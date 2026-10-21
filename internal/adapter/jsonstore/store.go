// Package jsonstore persists rooms and file metadata in one JSON file.
// It is a small stand-in for SQLite (planned for a later phase) and sits
// behind the same repository ports, so it can be swapped without touching use cases.
//
// Room PINs are stored in clear text because the owner needs to display them;
// the file is written with mode 0600.
package jsonstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"lanshare/internal/domain"
)

type roomDTO struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	PIN       string    `json:"pin"`
	CreatedAt time.Time `json:"createdAt"`
}

type fileDTO struct {
	ID           string    `json:"id"`
	RoomID       string    `json:"roomId"`
	Dir          string    `json:"dir,omitempty"`
	Name         string    `json:"name"`
	Size         int64     `json:"size"`
	UploaderID   string    `json:"uploaderId"`
	UploaderName string    `json:"uploaderName"`
	CreatedAt    time.Time `json:"createdAt"`
}

type stateDTO struct {
	Rooms []roomDTO `json:"rooms"`
	Files []fileDTO `json:"files"`
}

// Store implements port.RoomRepository and port.FileRepository.
type Store struct {
	path string

	mu    sync.RWMutex
	rooms map[domain.RoomID]domain.Room
	files map[domain.RoomID]map[domain.FileID]domain.File
}

// Open loads the state file at path, creating an empty store if it is missing.
func Open(path string) (*Store, error) {
	s := &Store{
		path:  path,
		rooms: make(map[domain.RoomID]domain.Room),
		files: make(map[domain.RoomID]map[domain.FileID]domain.File),
	}
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return s, nil
	case err != nil:
		return nil, err
	}
	var st stateDTO
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	for _, r := range st.Rooms {
		s.rooms[domain.RoomID(r.ID)] = domain.Room{
			ID: domain.RoomID(r.ID), Name: r.Name, PIN: r.PIN, CreatedAt: r.CreatedAt,
		}
	}
	for _, f := range st.Files {
		s.putFile(domain.File{
			ID: domain.FileID(f.ID), RoomID: domain.RoomID(f.RoomID), Dir: f.Dir, Name: f.Name, Size: f.Size,
			UploaderID: domain.DeviceID(f.UploaderID), UploaderName: f.UploaderName, CreatedAt: f.CreatedAt,
		})
	}
	return s, nil
}

func (s *Store) putFile(f domain.File) {
	m := s.files[f.RoomID]
	if m == nil {
		m = make(map[domain.FileID]domain.File)
		s.files[f.RoomID] = m
	}
	m[f.ID] = f
}

// saveLocked writes the state atomically. The caller holds s.mu.
func (s *Store) saveLocked() error {
	var st stateDTO
	for _, r := range s.rooms {
		st.Rooms = append(st.Rooms, roomDTO{string(r.ID), r.Name, r.PIN, r.CreatedAt})
	}
	for _, m := range s.files {
		for _, f := range m {
			st.Files = append(st.Files, fileDTO{
				ID: string(f.ID), RoomID: string(f.RoomID), Dir: f.Dir, Name: f.Name, Size: f.Size,
				UploaderID: string(f.UploaderID), UploaderName: f.UploaderName, CreatedAt: f.CreatedAt,
			})
		}
	}
	sort.Slice(st.Rooms, func(i, j int) bool { return st.Rooms[i].ID < st.Rooms[j].ID })
	sort.Slice(st.Files, func(i, j int) bool { return st.Files[i].ID < st.Files[j].ID })

	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".state-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}

// ---- RoomRepository ----

func (s *Store) Get(_ context.Context, id domain.RoomID) (domain.Room, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.rooms[id]
	if !ok {
		return domain.Room{}, domain.ErrNotFound
	}
	return r, nil
}

func (s *Store) List(_ context.Context) ([]domain.Room, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.Room, 0, len(s.rooms))
	for _, r := range s.rooms {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *Store) Save(_ context.Context, r domain.Room) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rooms[r.ID] = r
	return s.saveLocked()
}

// ---- FileRepository ----
// The file repository methods have different names so one Store can satisfy
// both interfaces; use RoomRepo() and FileRepo() to get the right view.

// RoomRepo returns the port.RoomRepository view of the store.
func (s *Store) RoomRepo() *Store { return s }

// FileRepo returns the port.FileRepository view of the store.
func (s *Store) FileRepo() *FileRepo { return &FileRepo{s: s} }

// FileRepo implements port.FileRepository on top of a Store.
type FileRepo struct{ s *Store }

func (r *FileRepo) Save(_ context.Context, f domain.File) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	r.s.putFile(f)
	return r.s.saveLocked()
}

func (r *FileRepo) Get(_ context.Context, room domain.RoomID, id domain.FileID) (domain.File, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	f, ok := r.s.files[room][id]
	if !ok {
		return domain.File{}, domain.ErrNotFound
	}
	return f, nil
}

func (r *FileRepo) ListByRoom(_ context.Context, room domain.RoomID) ([]domain.File, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	m := r.s.files[room]
	out := make([]domain.File, 0, len(m))
	for _, f := range m {
		out = append(out, f)
	}
	return out, nil
}

func (r *FileRepo) Delete(_ context.Context, room domain.RoomID, id domain.FileID) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	if _, ok := r.s.files[room][id]; !ok {
		return domain.ErrNotFound
	}
	delete(r.s.files[room], id)
	return r.s.saveLocked()
}
