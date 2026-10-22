// Package transfer implements streaming upload, download, zip download,
// thumbnails, listing and delete of files, and reports progress to the room.
package transfer

import (
	"archive/zip"
	"bytes"
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
	// State is "running", "interrupted" (an upload session lost its
	// connection and may resume), or a final "done", "failed" or "stopped".
	State string `json:"state"`
}

const (
	DirectionUpload   = "upload"
	DirectionDownload = "download"

	StateRunning     = "running"
	StateInterrupted = "interrupted"
	StateDone        = "done"
	StateFailed      = "failed"
	StateStopped     = "stopped"

	// MaxArchiveFiles bounds one zip download.
	MaxArchiveFiles = 500
	// ThumbSize is the longest side of generated thumbnails, in pixels.
	ThumbSize = 360
	// maxThumbJobs bounds concurrent (CPU and memory hungry) renders.
	maxThumbJobs = 2
	// streamBufSize is the copy window for zip entries and other streaming
	// downloads. Larger than the 1 MiB append buffer (Req 2.10) because this
	// path is not required to bound in-memory file content that tightly.
	streamBufSize = 4 << 20
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

// Service moves file content in and out of rooms. Besides the one-shot
// Upload it runs resumable upload sessions (see session.go); those need
// ConfigureUploads, and without it the service behaves as it did before.
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

	// rooms supplies per-room quota and file TTL. Optional: tests that do not
	// set it skip those checks.
	rooms port.RoomRepository

	// sys draws previews of formats the Go decoders cannot read; nil = none.
	// Set by SetSystemPreviewer before the service is shared.
	sys port.SystemPreviewer
	// heic turns published HEIC/HEIF files into JPEG; nil = leave as uploaded.
	heic port.HEICConverter
	// noPreview remembers files the system could not draw, so a slow or failing
	// tool is not asked again for the same file until the process restarts.
	noPreviewMu sync.Mutex
	noPreview   map[domain.FileID]struct{}

	// Resumable upload state. Set up by ConfigureUploads before the service
	// is shared between goroutines, read-only afterwards.
	upDeps UploadDeps
	upCfg  UploadConfig

	// regMu guards the registry fields below. It is held only for short map
	// operations, never across I/O. Lock order: regMu -> sessionRT.mu ->
	// roomLock -> leaf locks (storage.Guard, jsonstore, tracker.mu).
	regMu       sync.Mutex
	sessions    map[domain.SessionID]*sessionRT
	perDevice   map[domain.DeviceID]int // unfinished sessions by creator
	perRoom     map[domain.RoomID]int   // unfinished sessions by room
	roomLocks   map[domain.RoomID]*sync.Mutex
	completions map[domain.SessionID]completionRecord

	// sumSem lets one lazy checksum of an old file run at a time.
	sumSem chan struct{}
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
		jobs:        make(chan struct{}, maxThumbJobs),
		noPreview:   make(map[domain.FileID]struct{}),
		sessions:    make(map[domain.SessionID]*sessionRT),
		perDevice:   make(map[domain.DeviceID]int),
		perRoom:     make(map[domain.RoomID]int),
		roomLocks:   make(map[domain.RoomID]*sync.Mutex),
		completions: make(map[domain.SessionID]completionRecord),
		sumSem:      make(chan struct{}, 1),
	}
}

// List returns the files of the actor's room, newest first.
func (s *Service) List(ctx context.Context, actor domain.Device) ([]domain.File, error) {
	if !actor.CanDownload() {
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

// Upload streams in.Body into the room, without a sender checksum. It is
// UploadVerified (upload.go) with a nil checksum, so its signature and its
// callers are unchanged; the file ends up with Integrity unverified.
func (s *Service) Upload(ctx context.Context, actor domain.Device, in UploadInput) (domain.File, error) {
	return s.UploadVerified(ctx, actor, in, nil)
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
	if !actor.CanDownload() {
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
	// The download counts as activity (keeps the Mac awake) until Close.
	return Download{File: f, Content: &countingSeeker{rsc: rc, t: t, size: f.Size, end: s.activity().Begin()}}, nil
}

// Delete removes a file from the actor's room.
func (s *Service) Delete(ctx context.Context, actor domain.Device, id domain.FileID) error {
	if !actor.CanManage() {
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

// PurgeRoom removes every file of room. Only an Owner may; the room need not
// be the actor's current room, so a room can be emptied before it is deleted.
func (s *Service) PurgeRoom(ctx context.Context, actor domain.Device, room domain.RoomID) error {
	if !actor.CanManage() {
		return domain.ErrForbidden
	}
	s.cancelSessionsInRoom(ctx, room)
	list, err := s.files.ListByRoom(ctx, room)
	if err != nil {
		return err
	}
	for _, f := range list {
		_ = s.store.Delete(ctx, room, f.ID)
		_ = s.thumbs.DeleteThumb(ctx, room, f.ID)
		if err := s.files.Delete(ctx, room, f.ID); err != nil {
			return err
		}
		s.events.Publish(port.Event{Type: port.EventFileDeleted, RoomID: room, Data: f.ID})
	}
	return s.store.RemoveRoom(ctx, room)
}

func (s *Service) cancelSessionsInRoom(ctx context.Context, room domain.RoomID) {
	if !s.uploadsConfigured() {
		return
	}
	s.regMu.Lock()
	var ids []domain.SessionID
	for id, rt := range s.sessions {
		if rt != nil && rt.room == room {
			ids = append(ids, id)
		}
	}
	s.regMu.Unlock()
	for _, id := range ids {
		rt, err := s.lookupInRoom(room, id)
		if err != nil {
			continue
		}
		_ = s.cancel(ctx, rt)
	}
}

// SetSystemPreviewer enables previews of formats beyond JPEG, PNG, GIF and
// WebP. Call it before the service is shared between goroutines.
func (s *Service) SetSystemPreviewer(p port.SystemPreviewer) { s.sys = p }

// SetHEICConverter enables converting published HEIC/HEIF files to JPEG.
func (s *Service) SetHEICConverter(c port.HEICConverter) { s.heic = c }

// SetRooms enables per-room quota and automatic file expiry. Call it before
// the service is shared between goroutines.
func (s *Service) SetRooms(rooms port.RoomRepository) { s.rooms = rooms }

// CanPreview reports whether Thumbnail can be expected to draw a picture for
// name: always for the formats the Go decoders know, and for the others only
// when a system previewer is set.
func (s *Service) CanPreview(name string) bool {
	switch domain.PreviewOf(name) {
	case domain.PreviewNone:
		return false
	case domain.PreviewNative:
		return true
	}
	return s.sys != nil
}

// PreviewBytes draws a thumbnail JPEG of an unsaved file (USB phone files).
// Formats the Go decoders know are drawn in-process; others go to the system
// previewer. A file that cannot be previewed is domain.ErrUnsupported.
func (s *Service) PreviewBytes(ctx context.Context, name string, src io.Reader) ([]byte, error) {
	kind := domain.PreviewOf(name)
	if kind == domain.PreviewNone || (kind != domain.PreviewNative && s.sys == nil) {
		return nil, domain.ErrUnsupported
	}
	select {
	case s.jobs <- struct{}{}:
		defer func() { <-s.jobs }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if kind == domain.PreviewNative {
		return s.render.Render(src, ThumbSize)
	}
	return s.systemPreview(ctx, name, kind, src)
}

// Thumbnail returns a JPEG preview, rendering and caching it on first use.
// Formats the Go decoders know are drawn in-process; others go to the system
// previewer, and the result is normalized to a small JPEG by the same renderer.
// A file that cannot be previewed is domain.ErrUnsupported.
func (s *Service) Thumbnail(ctx context.Context, actor domain.Device, id domain.FileID) ([]byte, error) {
	if !actor.CanDownload() {
		return nil, domain.ErrForbidden
	}
	f, err := s.files.Get(ctx, actor.RoomID, id)
	if err != nil {
		return nil, err
	}
	kind := domain.PreviewOf(f.Name)
	if kind == domain.PreviewNone || (kind != domain.PreviewNative && s.sys == nil) {
		return nil, domain.ErrUnsupported
	}
	if b, err := s.thumbs.GetThumb(ctx, actor.RoomID, id); err == nil {
		return b, nil
	} else if !errors.Is(err, domain.ErrNotFound) {
		return nil, err
	}
	if s.previewFailed(id) {
		return nil, domain.ErrUnsupported
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

	var b []byte
	if kind == domain.PreviewNative {
		b, err = s.render.Render(src, ThumbSize)
	} else {
		b, err = s.systemPreview(ctx, f.Name, kind, src)
	}
	if err != nil {
		if errors.Is(err, domain.ErrUnsupported) && kind != domain.PreviewNative {
			s.markPreviewFailed(id)
		}
		return nil, err
	}
	_ = s.thumbs.PutThumb(ctx, actor.RoomID, id, b) // caching is best effort
	return b, nil
}

// systemPreview asks the host for a picture and shrinks it to a thumbnail JPEG.
func (s *Service) systemPreview(ctx context.Context, name string, kind domain.PreviewKind, src io.Reader) ([]byte, error) {
	img, err := s.sys.Preview(ctx, name, kind, src, ThumbSize)
	if err != nil {
		return nil, err
	}
	return s.render.Render(bytes.NewReader(img), ThumbSize)
}

func (s *Service) previewFailed(id domain.FileID) bool {
	s.noPreviewMu.Lock()
	defer s.noPreviewMu.Unlock()
	_, bad := s.noPreview[id]
	return bad
}

func (s *Service) markPreviewFailed(id domain.FileID) {
	s.noPreviewMu.Lock()
	defer s.noPreviewMu.Unlock()
	s.noPreview[id] = struct{}{}
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
	if !actor.CanDownload() {
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
	defer a.s.activity().Begin()() // the zip counts as activity until it is written
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
	buf := make([]byte, streamBufSize)
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
			_, err = io.CopyBuffer(dst, &countingReader{r: src, t: t}, buf)
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
//
// Invariants: Bytes never decreases (except through rewind, for a rolled-back
// upload request); exactly one final event (done, failed or
// stopped) is published and nothing after it. An upload session's tracker
// lives from its first append to the end of the session, so it can pass
// through "interrupted" and back to "running" any number of times.
type tracker struct {
	s     *Service
	actor domain.Device
	p     Progress
	mu    sync.Mutex
	last  time.Time
	lastN int64
	// closed is set by finish. announced is set once a running or
	// interrupted event has gone out, so cleanup knows whether the room has
	// an entry to close with a "stopped" event.
	closed    bool
	announced bool
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
	if n <= 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	now := t.s.clock.Now()
	if t.p.State == StateInterrupted {
		// Bytes are flowing again. Speed is measured from here, not across
		// the pause, and the next running event follows the usual throttle.
		t.p.State = StateRunning
		t.last, t.lastN = now, t.p.Bytes
	}
	t.p.Bytes += int64(n)
	elapsed := now.Sub(t.last)
	if elapsed < t.s.interval {
		return
	}
	t.p.Speed = int64(float64(t.p.Bytes-t.lastN) / elapsed.Seconds())
	t.last, t.lastN = now, t.p.Bytes
	t.publishLocked()
}

// rewind lowers the reported bytes to bytes when an upload request was rolled
// back after its bytes had been counted, so the room is never told about bytes
// the server does not hold. It is the only way Bytes decreases, it publishes
// nothing by itself, and it does nothing on a closed tracker or when Bytes is
// not above the given value.
func (t *tracker) rewind(bytes int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed || t.p.Bytes <= bytes {
		return
	}
	t.p.Bytes = bytes
	if t.lastN > bytes {
		t.lastN = bytes
	}
}

// mark moves the transfer to a non-final state and tells the room at once,
// bypassing the throttle. Only StateInterrupted is supported; it reports
// Speed 0 and keeps the tracker open. Marking a closed tracker, or one that is
// already interrupted, does nothing, so a duplicate event is never sent.
func (t *tracker) mark(state string) {
	if state != StateInterrupted {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed || t.p.State == StateInterrupted {
		return
	}
	t.p.State = StateInterrupted
	t.p.Speed = 0
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

// wasAnnounced reports whether the room has seen a running or interrupted
// event of this transfer.
func (t *tracker) wasAnnounced() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.announced
}

func (t *tracker) publishLocked() {
	if t.p.State == StateRunning || t.p.State == StateInterrupted {
		t.announced = true
	}
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
	// end closes the download's Active_Transfer; Close calls it once.
	end     func()
	endOnce sync.Once
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
	if c.end != nil {
		c.endOnce.Do(c.end)
	}
	return c.rsc.Close()
}
