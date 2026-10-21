// Package fake provides in-memory test doubles for the ports, so use cases
// can be tested without HTTP, disk or network.
package fake

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sort"
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
