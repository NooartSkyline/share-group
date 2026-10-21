// Package transfer implements streaming upload, download, zip download,
// thumbnails, listing and delete of files, and reports progress to the room.
package transfer

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"lanshare/internal/domain"
	"lanshare/internal/usecase/port"
)

// Progress is the payload of EventTransfer.
type Progress struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Direction  string `json:"direction"` // "upload" | "download"
	DeviceID   string `json:"deviceId"`
	DeviceName string `json:"deviceName"`
	Bytes      int64  `json:"bytes"`
	Total      int64  `json:"total"` // -1 when unknown
	Speed      int64  `json:"speed"` // bytes per second since the previous report
	State      string `json:"state"` // "running" | "done" | "failed" | "stopped"
}

const (
	DirectionUpload   = "upload"
	DirectionDownload = "download"

	StateRunning = "running"
	StateDone    = "done"
	StateFailed  = "failed"
	StateStopped = "stopped"

	// MaxArchiveFiles bounds one zip download.
	MaxArchiveFiles = 500
	// ThumbSize is the longest side of generated thumbnails, in pixels.
	ThumbSize = 360
	// maxThumbJobs bounds concurrent (CPU and memory hungry) renders.
	maxThumbJobs = 2
)

// ConflictPolicy says what to do when a file with the same folder and name
// already exists in the room.
type ConflictPolicy string

const (
	// ConflictRename keeps both files; the new one gets "name (2).ext". Default.
	ConflictRename ConflictPolicy = "rename"
	// ConflictOverwrite replaces the existing file. Only the room owner or
	// the device that uploaded the existing file may do that.
	ConflictOverwrite ConflictPolicy = "overwrite"
)

type Service struct {
	files    port.FileRepository
	store    port.FileStore
	thumbs   port.ThumbStore
	render   port.ThumbRenderer
	events   port.EventPublisher
	ids      port.IDGenerator
	clock    port.Clock
	interval time.Duration
	jobs     chan struct{}
}

// NewService builds the service. interval is the minimum time between two
// progress events of the same transfer.
func NewService(
	files port.FileRepository,
	store port.FileStore,
	thumbs port.ThumbStore,
	render port.ThumbRenderer,
	events port.EventPublisher,
	ids port.IDGenerator,
	clock port.Clock,
	interval time.Duration,
) *Service {
	return &Service{
		files: files, store: store, thumbs: thumbs, render: render,
		events: events, ids: ids, clock: clock, interval: interval,
		jobs: make(chan struct{}, maxThumbJobs),
	}
}

// List returns the files of the actor's room, newest first.
func (s *Service) List(ctx context.Context, actor domain.Device) ([]domain.File, error) {
	if !actor.Role.CanDownload() {
		return nil, domain.ErrForbidden
	}
	list, err := s.files.ListByRoom(ctx, actor.RoomID)
	if err != nil {
		return nil, err
	}
	sort.Slice(list, func(i, j int) bool { return list[i].CreatedAt.After(list[j].CreatedAt) })
	return list, nil
}

// UploadInput describes one file to store.
type UploadInput struct {
	Name     string
	Dir      string // folder the file was sent in, "" for none
	Size     int64  // expected length, or -1 when unknown
	Conflict ConflictPolicy
	Body     io.Reader
}

// Upload streams in.Body into the room.
func (s *Service) Upload(ctx context.Context, actor domain.Device, in UploadInput) (domain.File, error) {
	if !actor.Role.CanUpload() {
		return domain.File{}, domain.ErrForbidden
	}
	name := domain.SanitizeFileName(in.Name)
	dir := domain.SanitizeDir(in.Dir)

	existing, err := s.files.ListByRoom(ctx, actor.RoomID)
	if err != nil {
		return domain.File{}, err
	}
	var replace *domain.File
	if in.Conflict == ConflictOverwrite {
		for i := range existing {
			if sameFolder(existing[i], dir) && strings.EqualFold(existing[i].Name, name) {
				replace = &existing[i]
				break
			}
		}
		if replace != nil && !actor.Role.CanManage() && replace.UploaderID != actor.ID {
			return domain.File{}, domain.ErrForbidden
		}
	} else {
		name = domain.UniqueName(name, func(c string) bool {
			for _, f := range existing {
				if sameFolder(f, dir) && strings.EqualFold(f.Name, c) {
					return true
				}
			}
			return false
		})
	}

	id := domain.FileID(s.ids.NewID())
	t := s.newTracker(actor, string(id), name, DirectionUpload, in.Size)
	written, err := s.store.Put(ctx, actor.RoomID, id, &countingReader{r: in.Body, t: t})
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		t.finish(StateFailed)
		return domain.File{}, err
	}

	f := domain.File{
		ID:           id,
		RoomID:       actor.RoomID,
		Dir:          dir,
		Name:         name,
		Size:         written,
		UploaderID:   actor.ID,
		UploaderName: actor.Name,
		CreatedAt:    s.clock.Now(),
	}
	if err := s.files.Save(ctx, f); err != nil {
		_ = s.store.Delete(context.WithoutCancel(ctx), actor.RoomID, id)
		t.finish(StateFailed)
		return domain.File{}, err
	}
	t.finish(StateDone)
	if replace != nil {
		s.remove(context.WithoutCancel(ctx), actor.RoomID, replace.ID)
	}
	s.events.Publish(port.Event{Type: port.EventFileAdded, RoomID: actor.RoomID, Data: f.ID})
	return f, nil
}

func sameFolder(f domain.File, dir string) bool { return strings.EqualFold(f.Dir, dir) }

// remove deletes content, thumbnail and metadata and tells the room.
func (s *Service) remove(ctx context.Context, room domain.RoomID, id domain.FileID) {
	_ = s.store.Delete(ctx, room, id)
	_ = s.thumbs.DeleteThumb(ctx, room, id)
	if err := s.files.Delete(ctx, room, id); err == nil {
		s.events.Publish(port.Event{Type: port.EventFileDeleted, RoomID: room, Data: id})
	}
}

// Download is an opened file; the caller must Close it.
type Download struct {
	File    domain.File
	Content port.ReadSeekCloser
}

// Open returns the file content for streaming. Lookups are scoped to the
// actor's room, so other rooms' files are reported as not found.
func (s *Service) Open(ctx context.Context, actor domain.Device, id domain.FileID) (Download, error) {
	if !actor.Role.CanDownload() {
		return Download{}, domain.ErrForbidden
	}
	f, err := s.files.Get(ctx, actor.RoomID, id)
	if err != nil {
		return Download{}, err
	}
	rc, err := s.store.Open(ctx, actor.RoomID, id)
	if err != nil {
		return Download{}, err
	}
	t := s.newTracker(actor, string(id), f.Path(), DirectionDownload, f.Size)
	return Download{File: f, Content: &countingSeeker{rsc: rc, t: t, size: f.Size}}, nil
}

// Delete removes a file from the actor's room.
func (s *Service) Delete(ctx context.Context, actor domain.Device, id domain.FileID) error {
	if !actor.Role.CanManage() {
		return domain.ErrForbidden
	}
	if _, err := s.files.Get(ctx, actor.RoomID, id); err != nil {
		return err
	}
	if err := s.store.Delete(ctx, actor.RoomID, id); err != nil {
		return err
	}
	_ = s.thumbs.DeleteThumb(ctx, actor.RoomID, id)
	if err := s.files.Delete(ctx, actor.RoomID, id); err != nil {
		return err
	}
	s.events.Publish(port.Event{Type: port.EventFileDeleted, RoomID: actor.RoomID, Data: id})
	return nil
}

// Thumbnail returns a JPEG preview, rendering and caching it on first use.
func (s *Service) Thumbnail(ctx context.Context, actor domain.Device, id domain.FileID) ([]byte, error) {
	if !actor.Role.CanDownload() {
		return nil, domain.ErrForbidden
	}
	f, err := s.files.Get(ctx, actor.RoomID, id)
	if err != nil {
		return nil, err
	}
	if !domain.CanThumbnail(f.Name) {
		return nil, domain.ErrUnsupported
	}
	if b, err := s.thumbs.GetThumb(ctx, actor.RoomID, id); err == nil {
		return b, nil
	} else if !errors.Is(err, domain.ErrNotFound) {
		return nil, err
	}

	select { // bound concurrent renders
	case s.jobs <- struct{}{}:
		defer func() { <-s.jobs }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	// another request may have finished the same render while we waited
	if b, err := s.thumbs.GetThumb(ctx, actor.RoomID, id); err == nil {
		return b, nil
	}

	src, err := s.store.Open(ctx, actor.RoomID, id)
	if err != nil {
		return nil, err
	}
	defer src.Close()
	b, err := s.render.Render(src, ThumbSize)
	if err != nil {
		return nil, err
	}
	_ = s.thumbs.PutThumb(ctx, actor.RoomID, id, b) // caching is best effort
	return b, nil
}

// Archive is a validated zip download. Validation happens in PlanArchive so
// the caller can still answer with a normal error before any zip byte is sent.
type Archive struct {
	s     *Service
	actor domain.Device
	Files []domain.File
}

// PlanArchive checks that every id exists in the actor's room.
func (s *Service) PlanArchive(ctx context.Context, actor domain.Device, ids []domain.FileID) (*Archive, error) {
	if !actor.Role.CanDownload() {
		return nil, domain.ErrForbidden
	}
	if len(ids) == 0 || len(ids) > MaxArchiveFiles {
		return nil, fmt.Errorf("%w: choose 1 to %d files", domain.ErrInvalidInput, MaxArchiveFiles)
	}
	a := &Archive{s: s, actor: actor}
	for _, id := range ids {
		f, err := s.files.Get(ctx, actor.RoomID, id)
		if err != nil {
			return nil, err
		}
		a.Files = append(a.Files, f)
	}
	return a, nil
}

// TotalSize is the sum of the file sizes.
func (a *Archive) TotalSize() int64 {
	var n int64
	for _, f := range a.Files {
		n += f.Size
	}
	return n
}

// WriteTo streams the zip (stored, not compressed: LAN speed matters more
// than size) to w, keeping folder structure and making duplicate names unique.
func (a *Archive) WriteTo(ctx context.Context, w io.Writer) error {
	t := a.s.newTracker(a.actor, a.s.ids.NewID(), fmt.Sprintf("%d files.zip", len(a.Files)), DirectionDownload, a.TotalSize())
	err := a.write(ctx, w, t)
	switch {
	case err == nil:
		t.finish(StateDone)
	case errors.Is(err, context.Canceled):
		t.finish(StateStopped)
	default:
		t.finish(StateFailed)
	}
	return err
}

func (a *Archive) write(ctx context.Context, w io.Writer, t *tracker) error {
	zw := zip.NewWriter(w)
	used := map[string]bool{}
	for _, f := range a.Files {
		if err := ctx.Err(); err != nil {
			return err
		}
		entry := domain.UniqueName(f.Name, func(c string) bool {
			return used[strings.ToLower(joinPath(f.Dir, c))]
		})
		full := joinPath(f.Dir, entry)
		used[strings.ToLower(full)] = true

		src, err := a.s.store.Open(ctx, a.actor.RoomID, f.ID)
		if err != nil {
			return err
		}
		dst, err := zw.CreateHeader(&zip.FileHeader{Name: full, Method: zip.Store, Modified: f.CreatedAt})
		if err == nil {
			_, err = io.CopyBuffer(dst, &countingReader{r: src, t: t}, make([]byte, 1<<20))
		}
		src.Close()
		if err != nil {
			return err
		}
	}
	return zw.Close()
}

func joinPath(dir, name string) string {
	if dir == "" {
		return name
	}
	return dir + "/" + name
}

// tracker throttles and publishes progress events for one transfer.
type tracker struct {
	s      *Service
	actor  domain.Device
	p      Progress
	mu     sync.Mutex
	last   time.Time
	lastN  int64
	closed bool
}

func (s *Service) newTracker(actor domain.Device, id, name, dir string, total int64) *tracker {
	now := s.clock.Now()
	return &tracker{
		s:     s,
		actor: actor,
		p: Progress{
			ID: id, Name: name, Direction: dir,
			DeviceID: string(actor.ID), DeviceName: actor.Name,
			Total: total, State: StateRunning,
		},
		last: now,
	}
}

func (t *tracker) add(n int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	t.p.Bytes += int64(n)
	now := t.s.clock.Now()
	elapsed := now.Sub(t.last)
	if elapsed < t.s.interval {
		return
	}
	t.p.Speed = int64(float64(t.p.Bytes-t.lastN) / elapsed.Seconds())
	t.last, t.lastN = now, t.p.Bytes
	t.publishLocked()
}

func (t *tracker) finish(state string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	t.closed = true
	t.p.State = state
	t.p.Speed = 0
	t.publishLocked()
}

func (t *tracker) publishLocked() {
	t.s.events.Publish(port.Event{Type: port.EventTransfer, RoomID: t.actor.RoomID, Data: t.p})
}

type countingReader struct {
	r io.Reader
	t *tracker
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if n > 0 {
		c.t.add(n)
	}
	return n, err
}

type countingSeeker struct {
	rsc  port.ReadSeekCloser
	t    *tracker
	size int64
}

func (c *countingSeeker) Read(p []byte) (int, error) {
	n, err := c.rsc.Read(p)
	if n > 0 {
		c.t.add(n)
	}
	return n, err
}

func (c *countingSeeker) Seek(offset int64, whence int) (int64, error) {
	return c.rsc.Seek(offset, whence)
}

func (c *countingSeeker) Close() error {
	state := StateStopped
	c.t.mu.Lock()
	if c.size >= 0 && c.t.p.Bytes >= c.size {
		state = StateDone
	}
	c.t.mu.Unlock()
	c.t.finish(state)
	return c.rsc.Close()
}
