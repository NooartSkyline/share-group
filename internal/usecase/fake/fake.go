// Package fake provides in-memory test doubles for the ports, so use cases
// can be tested without HTTP, disk or network.
package fake

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"

	"lanshare/internal/domain"
	"lanshare/internal/usecase/port"
)

// Clock is a manually advanced clock.
type Clock struct {
	mu sync.Mutex
	T  time.Time
}

func NewClock() *Clock { return &Clock{T: time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)} }

func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.T
}

func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.T = c.T.Add(d)
}

// IDs returns id-1, id-2, ...
type IDs struct {
	mu sync.Mutex
	n  int
}

func (i *IDs) NewID() string {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.n++
	return fmt.Sprintf("id-%d", i.n)
}

// Publisher records published events.
type Publisher struct {
	mu     sync.Mutex
	Events []port.Event
}

func (p *Publisher) Publish(e port.Event) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Events = append(p.Events, e)
}

func (p *Publisher) Count(typ string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, e := range p.Events {
		if e.Type == typ {
			n++
		}
	}
	return n
}

// Rooms is an in-memory RoomRepository.
type Rooms struct {
	mu sync.Mutex
	M  map[domain.RoomID]domain.Room
}

func NewRooms(rooms ...domain.Room) *Rooms {
	r := &Rooms{M: map[domain.RoomID]domain.Room{}}
	for _, rm := range rooms {
		r.M[rm.ID] = rm
	}
	return r
}

func (r *Rooms) Get(_ context.Context, id domain.RoomID) (domain.Room, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rm, ok := r.M[id]
	if !ok {
		return domain.Room{}, domain.ErrNotFound
	}
	return rm, nil
}

func (r *Rooms) List(context.Context) ([]domain.Room, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []domain.Room
	for _, rm := range r.M {
		out = append(out, rm)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (r *Rooms) Save(_ context.Context, rm domain.Room) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.M[rm.ID] = rm
	return nil
}

func (r *Rooms) Delete(_ context.Context, id domain.RoomID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.M, id)
	return nil
}

// Files is an in-memory FileRepository.
type Files struct {
	mu sync.Mutex
	M  map[domain.RoomID]map[domain.FileID]domain.File
}

func NewFiles() *Files { return &Files{M: map[domain.RoomID]map[domain.FileID]domain.File{}} }

func (f *Files) Save(_ context.Context, file domain.File) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.M[file.RoomID] == nil {
		f.M[file.RoomID] = map[domain.FileID]domain.File{}
	}
	f.M[file.RoomID][file.ID] = file
	return nil
}

func (f *Files) Get(_ context.Context, room domain.RoomID, id domain.FileID) (domain.File, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	file, ok := f.M[room][id]
	if !ok {
		return domain.File{}, domain.ErrNotFound
	}
	return file, nil
}

func (f *Files) ListByRoom(_ context.Context, room domain.RoomID) ([]domain.File, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.File
	for _, file := range f.M[room] {
		out = append(out, file)
	}
	return out, nil
}

func (f *Files) Delete(_ context.Context, room domain.RoomID, id domain.FileID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.M[room][id]; !ok {
		return domain.ErrNotFound
	}
	delete(f.M[room], id)
	return nil
}

func (f *Files) SetChecksum(_ context.Context, room domain.RoomID, id domain.FileID, sha256hex string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	file, ok := f.M[room][id]
	if !ok {
		return domain.ErrNotFound
	}
	file.SHA256 = sha256hex
	f.M[room][id] = file
	return nil
}

// Store is an in-memory FileStore.
type Store struct {
	mu sync.Mutex
	M  map[string][]byte
}

func NewStore() *Store { return &Store{M: map[string][]byte{}} }

func key(room domain.RoomID, id domain.FileID) string { return string(room) + "/" + string(id) }

func (s *Store) Put(_ context.Context, room domain.RoomID, id domain.FileID, r io.Reader) (int64, error) {
	// Read in fixed 1 MiB chunks like the real store, so progress tests are deterministic.
	var out bytes.Buffer
	chunk := make([]byte, 1<<20)
	for {
		n, err := r.Read(chunk)
		out.Write(chunk[:n])
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, err // like the real store: nothing is kept on failure
		}
	}
	data := out.Bytes()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.M[key(room, id)] = data
	return int64(len(data)), nil
}

type readSeekCloser struct{ *bytes.Reader }

func (readSeekCloser) Close() error { return nil }

func (s *Store) Open(_ context.Context, room domain.RoomID, id domain.FileID) (port.ReadSeekCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.M[key(room, id)]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return readSeekCloser{bytes.NewReader(data)}, nil
}

func (s *Store) Delete(_ context.Context, room domain.RoomID, id domain.FileID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.M, key(room, id))
	return nil
}

func (s *Store) RemoveRoom(_ context.Context, room domain.RoomID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	prefix := string(room) + "/"
	for k := range s.M {
		if strings.HasPrefix(k, prefix) {
			delete(s.M, k)
		}
	}
	return nil
}

func (s *Store) Has(room domain.RoomID, id domain.FileID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.M[key(room, id)]
	return ok
}

// Thumbs is an in-memory ThumbStore.
type Thumbs struct {
	mu sync.Mutex
	M  map[string][]byte
}

func NewThumbs() *Thumbs { return &Thumbs{M: map[string][]byte{}} }

func (t *Thumbs) GetThumb(_ context.Context, room domain.RoomID, id domain.FileID) ([]byte, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	b, ok := t.M[key(room, id)]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return b, nil
}

func (t *Thumbs) PutThumb(_ context.Context, room domain.RoomID, id domain.FileID, data []byte) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.M[key(room, id)] = data
	return nil
}

func (t *Thumbs) DeleteThumb(_ context.Context, room domain.RoomID, id domain.FileID) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.M, key(room, id))
	return nil
}

func (t *Thumbs) Has(room domain.RoomID, id domain.FileID) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, ok := t.M[key(room, id)]
	return ok
}

// Renderer is a ThumbRenderer that returns "thumb:" + the input and counts calls.
type Renderer struct {
	mu    sync.Mutex
	Calls int
	Err   error
}

func (r *Renderer) Render(src io.Reader, _ int) ([]byte, error) {
	r.mu.Lock()
	r.Calls++
	err := r.Err
	r.mu.Unlock()
	if err != nil {
		return nil, err
	}
	b, err := io.ReadAll(src)
	if err != nil {
		return nil, err
	}
	return append([]byte("thumb:"), b...), nil
}

// Compile-time checks that every fake still satisfies its port.
var (
	_ port.Clock                   = (*Clock)(nil)
	_ port.IDGenerator             = (*IDs)(nil)
	_ port.EventPublisher          = (*Publisher)(nil)
	_ port.RoomRepository          = (*Rooms)(nil)
	_ port.FileRepository          = (*Files)(nil)
	_ port.FileStore               = (*Store)(nil)
	_ port.ThumbStore              = (*Thumbs)(nil)
	_ port.ThumbRenderer           = (*Renderer)(nil)
	_ port.UploadSessionRepository = (*Sessions)(nil)
	_ port.PartialStore            = (*Partials)(nil)
	_ port.PartialWriter           = (*partialWriter)(nil)
	_ port.DiskSpace               = (*Disk)(nil)
	_ port.SecretGenerator         = (*Secrets)(nil)
	_ port.ActivityTracker         = (*Activity)(nil)
	_ port.CaffeinateLauncher      = (*Launcher)(nil)
	_ port.CaffeinateProcess       = (*Process)(nil)
	_ port.CommandRunner           = (*Runner)(nil)
	_ port.PeerLog                 = (*PeerLog)(nil)
	_ port.LocalAddrProvider       = (*LocalAddrs)(nil)
)

// errInjected is the default error of fault injection helpers.
var errInjected = errors.New("fake: injected failure")

// Sessions is an in-memory UploadSessionRepository with fault injection:
// the k-th Save can fail, and a stored record can be turned into a corrupt one
// the way a half-written or tampered file would be.
type Sessions struct {
	mu      sync.Mutex
	m       map[string]domain.UploadSession
	corrupt map[string]string // key -> path reported by LoadAll
	saves   int
	failAt  int
	failErr error
	saveErr error // when set, every Save fails
	loadErr error
	delErr  error
	deletes int
}

func NewSessions() *Sessions {
	return &Sessions{m: map[string]domain.UploadSession{}, corrupt: map[string]string{}}
}

func sessKey(room domain.RoomID, id domain.SessionID) string { return string(room) + "/" + string(id) }

// FailSaveAt makes the k-th Save call (1-based, counted from now on) return
// err, or an injected error when err is nil. The record is not stored. Other
// calls are unaffected, so a test can fail exactly one persist.
func (s *Sessions) FailSaveAt(k int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err == nil {
		err = errInjected
	}
	s.saves, s.failAt, s.failErr = 0, k, err
}

// SetSaveErr makes every Save fail until it is called again with nil.
func (s *Sessions) SetSaveErr(err error) { s.mu.Lock(); s.saveErr = err; s.mu.Unlock() }

// SetDeleteErr makes every Delete fail until it is called again with nil.
func (s *Sessions) SetDeleteErr(err error) { s.mu.Lock(); s.delErr = err; s.mu.Unlock() }

// SetLoadErr makes LoadAll fail, as if the store could not be read.
func (s *Sessions) SetLoadErr(err error) { s.mu.Lock(); s.loadErr = err; s.mu.Unlock() }

func (s *Sessions) Save(_ context.Context, rec domain.UploadSession) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saves++
	if s.failAt > 0 && s.saves == s.failAt {
		return s.failErr
	}
	if s.saveErr != nil {
		return s.saveErr
	}
	rec.HashState = append([]byte(nil), rec.HashState...)
	k := sessKey(rec.RoomID, rec.ID)
	s.m[k] = rec
	delete(s.corrupt, k) // a fresh atomic Save replaces a bad record
	return nil
}

func (s *Sessions) Delete(_ context.Context, room domain.RoomID, id domain.SessionID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deletes++
	if s.delErr != nil {
		return s.delErr
	}
	k := sessKey(room, id)
	delete(s.m, k)
	delete(s.corrupt, k)
	return nil
}

// DeleteCorrupt removes the corrupt record that LoadAll reported at path. Like
// Delete it counts as a delete call and honours SetDeleteErr.
func (s *Sessions) DeleteCorrupt(_ context.Context, path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deletes++
	if s.delErr != nil {
		return s.delErr
	}
	for k, p := range s.corrupt {
		if p == path {
			delete(s.corrupt, k)
		}
	}
	return nil
}

func (s *Sessions) LoadAll(context.Context) ([]domain.UploadSession, []string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loadErr != nil {
		return nil, nil, s.loadErr
	}
	var ok []domain.UploadSession
	for _, rec := range s.m {
		rec.HashState = append([]byte(nil), rec.HashState...)
		ok = append(ok, rec)
	}
	sort.Slice(ok, func(i, j int) bool { return sessKey(ok[i].RoomID, ok[i].ID) < sessKey(ok[j].RoomID, ok[j].ID) })
	var bad []string
	for _, p := range s.corrupt {
		bad = append(bad, p)
	}
	sort.Strings(bad)
	return ok, bad, nil
}

// MarkCorrupt turns the stored record (if any) into a corrupt one: LoadAll
// stops returning it and reports its path instead. Delete removes it.
func (s *Sessions) MarkCorrupt(room domain.RoomID, id domain.SessionID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := sessKey(room, id)
	delete(s.m, k)
	s.corrupt[k] = "sessions/" + k + ".json"
}

// Get returns the stored record without counting as a port call.
func (s *Sessions) Get(room domain.RoomID, id domain.SessionID) (domain.UploadSession, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.m[sessKey(room, id)]
	return rec, ok
}

// Len is the number of good and corrupt records currently held.
func (s *Sessions) Len() (good, corrupt int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.m), len(s.corrupt)
}

// DeleteCalls counts Delete calls seen so far, failed ones included.
func (s *Sessions) DeleteCalls() int { s.mu.Lock(); defer s.mu.Unlock(); return s.deletes }

// LegacyTemp is a leftover temp file of the old upload path (rooms/*/.upload-*).
type LegacyTemp struct {
	Room    domain.RoomID
	Name    string
	ModTime time.Time
}

// Partials is an in-memory PartialStore. It keeps real bytes, so truncating
// and appending behave like files, and it can run out of space after a
// number of bytes. Bytes handed back through ReadAll are counted, which lets
// tests prove that resuming never re-reads a partial (Req 8.3).
type Partials struct {
	mu       sync.Mutex
	data     map[string][]byte
	promoted map[string][]byte // final file content, keyed like Store
	legacy   []LegacyTemp
	// Final, when set, receives promoted content so the same bytes show up in
	// the fake FileStore; Demote takes them back.
	Final *Store

	budget    int64 // bytes still writable; negative means unlimited
	limited   bool
	syncErr   error
	createErr error
	truncErr  error
	promoErr  error
	reread    int64
	syncs     int
	writes    int64
}

func NewPartials() *Partials {
	return &Partials{data: map[string][]byte{}, promoted: map[string][]byte{}}
}

// FailAfterBytes lets n more bytes be written (summed over all partials) and
// then fails the write that crosses that point with domain.ErrDiskFull; the
// bytes up to the limit are kept, like a real disk. n = 0 fails the next
// write. Pass a negative n to remove the limit.
func (p *Partials) FailAfterBytes(n int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.limited = n >= 0
	p.budget = n
}

// SetSyncErr, SetCreateErr, SetTruncateErr and SetPromoteErr make the matching
// operation fail until set again with nil.
func (p *Partials) SetSyncErr(err error)     { p.mu.Lock(); p.syncErr = err; p.mu.Unlock() }
func (p *Partials) SetCreateErr(err error)   { p.mu.Lock(); p.createErr = err; p.mu.Unlock() }
func (p *Partials) SetTruncateErr(err error) { p.mu.Lock(); p.truncErr = err; p.mu.Unlock() }
func (p *Partials) SetPromoteErr(err error)  { p.mu.Lock(); p.promoErr = err; p.mu.Unlock() }

func (p *Partials) Create(_ context.Context, room domain.RoomID, id domain.SessionID) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.createErr != nil {
		return p.createErr
	}
	k := sessKey(room, id)
	if _, ok := p.data[k]; ok {
		return fmt.Errorf("partial %s already exists: %w", k, domain.ErrInvalidInput)
	}
	p.data[k] = []byte{}
	return nil
}

func (p *Partials) OpenAppend(_ context.Context, room domain.RoomID, id domain.SessionID, offset int64) (port.PartialWriter, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	k := sessKey(room, id)
	b, ok := p.data[k]
	if !ok {
		return nil, domain.ErrNotFound
	}
	if offset < 0 || offset > int64(len(b)) {
		return nil, fmt.Errorf("partial %s has %d bytes, cannot append at %d: %w", k, len(b), offset, domain.ErrInvalidInput)
	}
	p.data[k] = b[:offset:offset]
	return &partialWriter{p: p, key: k}, nil
}

func (p *Partials) Size(_ context.Context, room domain.RoomID, id domain.SessionID) (int64, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	b, ok := p.data[sessKey(room, id)]
	if !ok {
		return 0, domain.ErrNotFound
	}
	return int64(len(b)), nil
}

func (p *Partials) Truncate(_ context.Context, room domain.RoomID, id domain.SessionID, size int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.truncErr != nil {
		return p.truncErr
	}
	k := sessKey(room, id)
	b, ok := p.data[k]
	if !ok {
		return domain.ErrNotFound
	}
	if size < 0 || size > int64(len(b)) {
		return fmt.Errorf("partial %s has %d bytes, cannot truncate to %d: %w", k, len(b), size, domain.ErrInvalidInput)
	}
	p.data[k] = b[:size:size]
	return nil
}

func (p *Partials) Delete(_ context.Context, room domain.RoomID, id domain.SessionID) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.data, sessKey(room, id))
	return nil
}

func (p *Partials) Promote(_ context.Context, room domain.RoomID, id domain.SessionID, file domain.FileID) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.promoErr != nil {
		return p.promoErr
	}
	k := sessKey(room, id)
	b, ok := p.data[k]
	if !ok {
		return domain.ErrNotFound
	}
	delete(p.data, k)
	if p.Final != nil {
		p.Final.mu.Lock()
		p.Final.M[key(room, file)] = b
		p.Final.mu.Unlock()
		return nil
	}
	p.promoted[key(room, file)] = b
	return nil
}

func (p *Partials) Demote(_ context.Context, room domain.RoomID, file domain.FileID, id domain.SessionID) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	fk := key(room, file)
	var b []byte
	var ok bool
	if p.Final != nil {
		p.Final.mu.Lock()
		b, ok = p.Final.M[fk]
		delete(p.Final.M, fk)
		p.Final.mu.Unlock()
	} else {
		b, ok = p.promoted[fk]
		delete(p.promoted, fk)
	}
	if !ok {
		return domain.ErrNotFound
	}
	p.data[sessKey(room, id)] = b
	return nil
}

func (p *Partials) List(context.Context) ([]port.PartialRef, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []port.PartialRef
	for k, b := range p.data {
		room, id, _ := strings.Cut(k, "/")
		out = append(out, port.PartialRef{Room: domain.RoomID(room), ID: domain.SessionID(id), Size: int64(len(b))})
	}
	sort.Slice(out, func(i, j int) bool { return sessKey(out[i].Room, out[i].ID) < sessKey(out[j].Room, out[j].ID) })
	return out, nil
}

func (p *Partials) SweepLegacyTemp(_ context.Context, before time.Time) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	kept := p.legacy[:0:0]
	removed := 0
	for _, l := range p.legacy {
		if l.ModTime.Before(before) {
			removed++
			continue
		}
		kept = append(kept, l)
	}
	p.legacy = kept
	return removed, nil
}

// ReadAll returns a copy of a partial's bytes and counts them as re-read.
// Code under test that must not re-read the partial never calls it; a test
// (or a test double of the hasher) calls it to model such a read.
func (p *Partials) ReadAll(_ context.Context, room domain.RoomID, id domain.SessionID) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	b, ok := p.data[sessKey(room, id)]
	if !ok {
		return nil, domain.ErrNotFound
	}
	p.reread += int64(len(b))
	return append([]byte(nil), b...), nil
}

// RereadBytes is the total number of bytes returned by ReadAll.
func (p *Partials) RereadBytes() int64 { p.mu.Lock(); defer p.mu.Unlock(); return p.reread }

// WrittenBytes is the total number of bytes accepted by all writers.
func (p *Partials) WrittenBytes() int64 { p.mu.Lock(); defer p.mu.Unlock(); return p.writes }

// SyncCalls counts Sync calls on all writers.
func (p *Partials) SyncCalls() int { p.mu.Lock(); defer p.mu.Unlock(); return p.syncs }

// Bytes returns a copy of a partial without counting it as a read.
func (p *Partials) Bytes(room domain.RoomID, id domain.SessionID) ([]byte, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	b, ok := p.data[sessKey(room, id)]
	return append([]byte(nil), b...), ok
}

// Has reports whether the partial exists.
func (p *Partials) Has(room domain.RoomID, id domain.SessionID) bool {
	_, ok := p.Bytes(room, id)
	return ok
}

// Put creates or replaces a partial with the given bytes, like a file left on
// disk by an earlier run. It does not count against FailAfterBytes.
func (p *Partials) Put(room domain.RoomID, id domain.SessionID, data []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.data[sessKey(room, id)] = append([]byte(nil), data...)
}

// AddLegacyTemp registers a leftover rooms/*/.upload-* file.
func (p *Partials) AddLegacyTemp(l LegacyTemp) {
	p.mu.Lock()
	p.legacy = append(p.legacy, l)
	p.mu.Unlock()
}

// LegacyTemps returns the leftover temp files still present.
func (p *Partials) LegacyTemps() []LegacyTemp {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]LegacyTemp(nil), p.legacy...)
}

type partialWriter struct {
	p      *Partials
	key    string
	closed bool
}

func (w *partialWriter) Write(b []byte) (int, error) {
	w.p.mu.Lock()
	defer w.p.mu.Unlock()
	if w.closed {
		return 0, errors.New("fake: write on closed partial writer")
	}
	cur, ok := w.p.data[w.key]
	if !ok {
		return 0, domain.ErrNotFound // deleted underneath the writer
	}
	n, err := len(b), error(nil)
	if w.p.limited && int64(n) > w.p.budget {
		n, err = int(w.p.budget), domain.ErrDiskFull
	}
	if w.p.limited {
		w.p.budget -= int64(n)
	}
	w.p.data[w.key] = append(cur, b[:n]...)
	w.p.writes += int64(n)
	return n, err
}

func (w *partialWriter) Sync() error {
	w.p.mu.Lock()
	defer w.p.mu.Unlock()
	w.p.syncs++
	return w.p.syncErr
}

func (w *partialWriter) Close() error {
	w.p.mu.Lock()
	defer w.p.mu.Unlock()
	w.closed = true
	return nil
}

// DiskImage describes what a data directory holds after a crash, so Recover
// can be tested against any mix of good, bad, missing and orphaned pieces.
type DiskImage struct {
	Sessions []domain.UploadSession // good records
	Corrupt  []domain.UploadSession // records that exist but cannot be parsed
	// Partials maps a session to the bytes of its partial file; a session with
	// no entry has no partial, and an entry with no session is an orphan.
	Partials map[domain.SessionID][]byte
	// PartialRoom is the room of orphan partials, which have no record to name it.
	PartialRoom domain.RoomID
	Legacy      []LegacyTemp
}

// Apply writes the image into the fakes.
func (img DiskImage) Apply(s *Sessions, p *Partials) {
	room := map[domain.SessionID]domain.RoomID{}
	for _, rec := range img.Sessions {
		_ = s.Save(context.Background(), rec)
		room[rec.ID] = rec.RoomID
	}
	for _, rec := range img.Corrupt {
		_ = s.Save(context.Background(), rec)
		s.MarkCorrupt(rec.RoomID, rec.ID)
		room[rec.ID] = rec.RoomID
	}
	for id, data := range img.Partials {
		r, ok := room[id]
		if !ok {
			r = img.PartialRoom
		}
		p.Put(r, id, data)
	}
	for _, l := range img.Legacy {
		p.AddLegacyTemp(l)
	}
}

// Disk is a DiskSpace whose free space is set by the test.
type Disk struct {
	mu    sync.Mutex
	free  uint64
	err   error
	calls int
}

func NewDisk(free uint64) *Disk { return &Disk{free: free} }

// SetFree changes the reported free bytes.
func (d *Disk) SetFree(free uint64) { d.mu.Lock(); d.free = free; d.mu.Unlock() }

// SetErr makes Free fail, as if the drive could not be read; nil restores it.
func (d *Disk) SetErr(err error) { d.mu.Lock(); d.err = err; d.mu.Unlock() }

// Calls is how many times Free was asked.
func (d *Disk) Calls() int { d.mu.Lock(); defer d.mu.Unlock(); return d.calls }

func (d *Disk) Free(context.Context) (uint64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls++
	if d.err != nil {
		return 0, d.err
	}
	return d.free, nil
}

// Secrets hands out queued values first, then key-N values that are unique,
// unguessable-looking and at least 32 characters long (the real ones are 32
// hex characters).
type Secrets struct {
	mu     sync.Mutex
	n      int
	queue  []string
	issued []string
}

// Queue makes the next NewSecret calls return these values in order.
func (s *Secrets) Queue(values ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queue = append(s.queue, values...)
}

func (s *Secrets) NewSecret() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var v string
	if len(s.queue) > 0 {
		v, s.queue = s.queue[0], s.queue[1:]
	} else {
		s.n++
		prefix := fmt.Sprintf("key-%d-", s.n)
		sum := sha256.Sum256([]byte(prefix))
		// 32 hex characters after the prefix keep every 8-character window
		// distinct, which the "key never appears in logs" scan relies on.
		v = fmt.Sprintf("%s%x", prefix, sum[:16])
	}
	s.issued = append(s.issued, v)
	return v
}

// Issued returns every secret handed out so far, in order.
func (s *Secrets) Issued() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.issued...)
}

// ActivityEvent is one call seen by Activity.
type ActivityEvent struct {
	Kind string // "begin", "end", "hold", "release"
	Key  string
	TTL  time.Duration
}

// Activity records Begin, End, Hold and Release in call order.
type Activity struct {
	mu     sync.Mutex
	Log    []ActivityEvent
	active int
	// Holds is the TTL of each key currently held.
	Holds map[string]time.Duration
	// DoubleEnds counts end functions called more than once, which breaks the
	// "exactly one end per Begin" rule.
	DoubleEnds int
}

func NewActivity() *Activity { return &Activity{Holds: map[string]time.Duration{}} }

func (a *Activity) Begin() func() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.active++
	a.Log = append(a.Log, ActivityEvent{Kind: "begin"})
	var once sync.Once
	return func() {
		ran := false
		once.Do(func() {
			ran = true
			a.mu.Lock()
			defer a.mu.Unlock()
			a.active--
			a.Log = append(a.Log, ActivityEvent{Kind: "end"})
		})
		if !ran {
			a.mu.Lock()
			a.DoubleEnds++
			a.mu.Unlock()
		}
	}
}

func (a *Activity) Hold(key string, ttl time.Duration) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.Holds == nil {
		a.Holds = map[string]time.Duration{}
	}
	a.Holds[key] = ttl
	a.Log = append(a.Log, ActivityEvent{Kind: "hold", Key: key, TTL: ttl})
}

func (a *Activity) Release(key string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.Holds, key)
	a.Log = append(a.Log, ActivityEvent{Kind: "release", Key: key})
}

// Active is the number of Begin calls not yet ended.
func (a *Activity) Active() int { a.mu.Lock(); defer a.mu.Unlock(); return a.active }

// Count returns how many events of the given kind were recorded.
func (a *Activity) Count(kind string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := 0
	for _, e := range a.Log {
		if e.Kind == kind {
			n++
		}
	}
	return n
}

// Process is a fake sleep-prevention process.
type Process struct {
	mu      sync.Mutex
	pid     int
	exited  chan error
	done    bool
	stopped bool
	stopErr error
}

func (p *Process) Exited() <-chan error { return p.exited }

// Stop ends the process once; later calls return the same result. The exit
// channel receives nil, as for a process that was signalled and exited.
func (p *Process) Stop(context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.done {
		p.done, p.stopped = true, true
		p.exited <- nil
		close(p.exited)
	}
	return p.stopErr
}

// Exit makes the process end by itself with err, as if it crashed or was
// killed from outside. It does nothing when the process already ended.
func (p *Process) Exit(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.done {
		return
	}
	p.done = true
	p.exited <- err
	close(p.exited)
}

// PID is the pid the process was started for.
func (p *Process) PID() int { return p.pid }

// Running reports whether the process has neither been stopped nor exited.
func (p *Process) Running() bool { p.mu.Lock(); defer p.mu.Unlock(); return !p.done }

// Stopped reports whether Stop ended it (as opposed to Exit).
func (p *Process) Stopped() bool { p.mu.Lock(); defer p.mu.Unlock(); return p.stopped }

// SetStopErr makes Stop return err.
func (p *Process) SetStopErr(err error) { p.mu.Lock(); p.stopErr = err; p.mu.Unlock() }

// Launcher is a CaffeinateLauncher driven by a script.
type Launcher struct {
	mu     sync.Mutex
	script []error // consumed one per Start; nil entry means success
	// StartErr is returned by every Start once the script is used up.
	StartErr error
	procs    []*Process
	starts   int
}

// Script queues results for the next Start calls: nil starts a process, an
// error makes Start fail.
func (l *Launcher) Script(results ...error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.script = append(l.script, results...)
}

func (l *Launcher) Start(pid int) (port.CaffeinateProcess, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.starts++
	err := l.StartErr
	if len(l.script) > 0 {
		err, l.script = l.script[0], l.script[1:]
	}
	if err != nil {
		return nil, err
	}
	p := &Process{pid: pid, exited: make(chan error, 1)}
	l.procs = append(l.procs, p)
	return p, nil
}

// Starts counts Start calls, failed ones included.
func (l *Launcher) Starts() int { l.mu.Lock(); defer l.mu.Unlock(); return l.starts }

// Processes returns every process started successfully, oldest first.
func (l *Launcher) Processes() []*Process {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]*Process(nil), l.procs...)
}

// Running is the number of started processes that are still alive.
func (l *Launcher) Running() int {
	n := 0
	for _, p := range l.Processes() {
		if p.Running() {
			n++
		}
	}
	return n
}

// Last returns the newest started process, or nil.
func (l *Launcher) Last() *Process {
	ps := l.Processes()
	if len(ps) == 0 {
		return nil
	}
	return ps[len(ps)-1]
}

// RunResult is the scripted answer of Runner for one command.
type RunResult struct {
	Out   []byte
	Err   error
	Delay time.Duration // wait this long (or until ctx ends) before answering
	Block bool          // wait until ctx ends, then return its error
}

// RunCall is one command seen by Runner.
type RunCall struct {
	Name string
	Args []string
}

// Runner is a CommandRunner that answers from a script. A command is looked
// up by its full text ("name arg1 arg2") first, then by name alone; anything
// unscripted fails, so a test never silently runs a real command.
type Runner struct {
	mu      sync.Mutex
	results map[string]RunResult
	calls   []RunCall
}

func NewRunner() *Runner { return &Runner{results: map[string]RunResult{}} }

// On scripts the answer for a command line such as "/usr/bin/pmset -g" or,
// with only a name, for every call of that command.
func (r *Runner) On(commandLine string, res RunResult) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.results[commandLine] = res
}

func (r *Runner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	line := strings.Join(append([]string{name}, args...), " ")
	r.mu.Lock()
	r.calls = append(r.calls, RunCall{Name: name, Args: append([]string(nil), args...)})
	res, ok := r.results[line]
	if !ok {
		res, ok = r.results[name]
	}
	r.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("fake runner: no script for %q", line)
	}
	if res.Block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if res.Delay > 0 {
		t := time.NewTimer(res.Delay)
		defer t.Stop()
		select {
		case <-t.C:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if res.Err != nil {
		return nil, res.Err
	}
	return append([]byte(nil), res.Out...), nil
}

// Calls returns the commands run so far, in order.
func (r *Runner) Calls() []RunCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]RunCall(nil), r.calls...)
}

// PeerLog is an in-memory PeerLog that keeps the latest time per address.
type PeerLog struct {
	mu   sync.Mutex
	seen map[netip.Addr]time.Time
}

func NewPeerLog() *PeerLog { return &PeerLog{seen: map[netip.Addr]time.Time{}} }

func (l *PeerLog) Record(ip netip.Addr, at time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.seen == nil {
		l.seen = map[netip.Addr]time.Time{}
	}
	if at.After(l.seen[ip]) {
		l.seen[ip] = at
	}
}

func (l *PeerLog) Since(t time.Time) []port.PeerSeen {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []port.PeerSeen
	for ip, at := range l.seen {
		if !at.Before(t) {
			out = append(out, port.PeerSeen{Addr: ip, LastSeen: at})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Addr.Less(out[j].Addr) })
	return out
}

// LocalAddrs is a LocalAddrProvider with a fixed list.
type LocalAddrs struct {
	mu    sync.Mutex
	addrs []netip.Addr
	err   error
}

func NewLocalAddrs(addrs ...netip.Addr) *LocalAddrs { return &LocalAddrs{addrs: addrs} }

// Set replaces the addresses and the error (nil for none).
func (l *LocalAddrs) Set(err error, addrs ...netip.Addr) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.addrs, l.err = addrs, err
}

func (l *LocalAddrs) LocalAddrs(context.Context) ([]netip.Addr, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err != nil {
		return nil, l.err
	}
	return append([]netip.Addr(nil), l.addrs...), nil
}

// HostFS is an in-memory HostBrowser.
type HostFS struct {
	mu      sync.Mutex
	Vols    []port.HostVolume
	Entries map[string][]port.HostEntry // volumeID + "\x00" + rel
	Files   map[string][]byte           // volumeID + "\x00" + rel
}

func (h *HostFS) key(volumeID, rel string) string {
	return volumeID + "\x00" + strings.Trim(rel, "/")
}

func (h *HostFS) Volumes(context.Context) ([]port.HostVolume, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]port.HostVolume(nil), h.Vols...), nil
}

func (h *HostFS) List(_ context.Context, volumeID, rel string) ([]port.HostEntry, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]port.HostEntry(nil), h.Entries[h.key(volumeID, rel)]...), nil
}

func (h *HostFS) Open(_ context.Context, volumeID, rel string) (port.ReadSeekCloser, int64, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	b, ok := h.Files[h.key(volumeID, rel)]
	if !ok {
		return nil, 0, domain.ErrNotFound
	}
	return newBytesFile(b), int64(len(b)), nil
}

func (h *HostFS) Put(_ context.Context, volumeID, rel string, r io.Reader) (int64, error) {
	if r == nil {
		return 0, domain.ErrInvalidInput
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return 0, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.Files == nil {
		h.Files = map[string][]byte{}
	}
	if h.Entries == nil {
		h.Entries = map[string][]port.HostEntry{}
	}
	rel = strings.Trim(rel, "/")
	h.Files[h.key(volumeID, rel)] = b
	parent, name := "", rel
	if i := strings.LastIndex(rel, "/"); i >= 0 {
		parent, name = rel[:i], rel[i+1:]
	}
	pk := h.key(volumeID, parent)
	found := false
	for i, e := range h.Entries[pk] {
		if e.Name == name {
			h.Entries[pk][i] = port.HostEntry{Name: name, Size: int64(len(b))}
			found = true
			break
		}
	}
	if !found {
		h.Entries[pk] = append(h.Entries[pk], port.HostEntry{Name: name, Size: int64(len(b))})
	}
	return int64(len(b)), nil
}

func (h *HostFS) Mkdir(_ context.Context, volumeID, rel, name string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.Entries == nil {
		h.Entries = map[string][]port.HostEntry{}
	}
	parent := h.key(volumeID, rel)
	h.Entries[parent] = append(h.Entries[parent], port.HostEntry{Name: name, Dir: true})
	childRel := name
	if rel != "" {
		childRel = rel + "/" + name
	}
	h.Entries[h.key(volumeID, childRel)] = []port.HostEntry{}
	return nil
}

func (h *HostFS) Remove(_ context.Context, volumeID, rel string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.Files != nil {
		delete(h.Files, h.key(volumeID, rel))
	}
	if h.Entries != nil {
		delete(h.Entries, h.key(volumeID, rel))
	}
	return nil
}

type bytesFile struct {
	*bytes.Reader
}

func newBytesFile(b []byte) *bytesFile {
	return &bytesFile{Reader: bytes.NewReader(append([]byte(nil), b...))}
}

func (*bytesFile) Close() error { return nil }
