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
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	PIN             string    `json:"pin"`
	CreatedAt       time.Time `json:"createdAt"`
	QuotaBytes      int64     `json:"quotaBytes,omitempty"`
	FileTTLNanos    int64     `json:"fileTTLNanos,omitempty"`
	RequireApproval bool      `json:"requireApproval,omitempty"`
	GuestKey        string    `json:"guestKey,omitempty"`
	GuestUntil      time.Time `json:"guestUntil,omitempty"`
	GuestMax        int64     `json:"guestMax,omitempty"`
}

type deviceDTO struct {
	ID         string    `json:"id"`
	RoomID     string    `json:"roomId"`
	Name       string    `json:"name"`
	Platform   string    `json:"platform"`
	Role       string    `json:"role"`
	RemoteAddr string    `json:"remoteAddr,omitempty"`
	JoinedAt   time.Time `json:"joinedAt"`
	Pending    bool      `json:"pending,omitempty"`
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
	SHA256       string    `json:"sha256,omitempty"`
	Integrity    string    `json:"integrity,omitempty"`
}

type stateDTO struct {
	Rooms   []roomDTO   `json:"rooms"`
	Files   []fileDTO   `json:"files"`
	Devices []deviceDTO `json:"devices,omitempty"`
}

// Store implements port.RoomRepository, port.FileRepository and port.DeviceRepository.
type Store struct {
	path string

	mu      sync.RWMutex
	rooms   map[domain.RoomID]domain.Room
	files   map[domain.RoomID]map[domain.FileID]domain.File
	devices map[domain.DeviceID]domain.Device
}

// Open loads the state file at path, creating an empty store if it is missing.
func Open(path string) (*Store, error) {
	s := &Store{
		path:    path,
		rooms:   make(map[domain.RoomID]domain.Room),
		files:   make(map[domain.RoomID]map[domain.FileID]domain.File),
		devices: make(map[domain.DeviceID]domain.Device),
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
			QuotaBytes: r.QuotaBytes, FileTTL: time.Duration(r.FileTTLNanos),
			RequireApproval: r.RequireApproval, GuestKey: r.GuestKey,
			GuestUntil: r.GuestUntil, GuestMax: r.GuestMax,
		}
	}
	for _, f := range st.Files {
		s.putFile(domain.File{
			ID: domain.FileID(f.ID), RoomID: domain.RoomID(f.RoomID), Dir: f.Dir, Name: f.Name, Size: f.Size,
			UploaderID: domain.DeviceID(f.UploaderID), UploaderName: f.UploaderName, CreatedAt: f.CreatedAt,
			SHA256: f.SHA256, Integrity: domain.Integrity(f.Integrity),
		})
	}
	for _, d := range st.Devices {
		s.devices[domain.DeviceID(d.ID)] = domain.Device{
			ID: domain.DeviceID(d.ID), RoomID: domain.RoomID(d.RoomID),
			Name: d.Name, Platform: d.Platform, Role: domain.Role(d.Role),
			RemoteAddr: d.RemoteAddr, JoinedAt: d.JoinedAt, Pending: d.Pending,
		}
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
		st.Rooms = append(st.Rooms, roomDTO{
			ID: string(r.ID), Name: r.Name, PIN: r.PIN, CreatedAt: r.CreatedAt,
			QuotaBytes: r.QuotaBytes, FileTTLNanos: int64(r.FileTTL),
			RequireApproval: r.RequireApproval, GuestKey: r.GuestKey,
			GuestUntil: r.GuestUntil, GuestMax: r.GuestMax,
		})
	}
	for _, m := range s.files {
		for _, f := range m {
			st.Files = append(st.Files, fileDTO{
				ID: string(f.ID), RoomID: string(f.RoomID), Dir: f.Dir, Name: f.Name, Size: f.Size,
				UploaderID: string(f.UploaderID), UploaderName: f.UploaderName, CreatedAt: f.CreatedAt,
				SHA256: f.SHA256, Integrity: string(f.Integrity),
			})
		}
	}
	for _, d := range s.devices {
		st.Devices = append(st.Devices, deviceDTO{
			ID: string(d.ID), RoomID: string(d.RoomID), Name: d.Name, Platform: d.Platform,
			Role: string(d.Role), RemoteAddr: d.RemoteAddr, JoinedAt: d.JoinedAt, Pending: d.Pending,
		})
	}
	sort.Slice(st.Rooms, func(i, j int) bool { return st.Rooms[i].ID < st.Rooms[j].ID })
	sort.Slice(st.Files, func(i, j int) bool { return st.Files[i].ID < st.Files[j].ID })
	sort.Slice(st.Devices, func(i, j int) bool { return st.Devices[i].ID < st.Devices[j].ID })

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

// SetChecksum records the digest under the same lock as Save, scanning only
// the given room. If persisting fails the in-memory change is rolled back so
// memory and state.json never disagree.
func (r *FileRepo) SetChecksum(_ context.Context, room domain.RoomID, id domain.FileID, sha256hex string) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	f, ok := r.s.files[room][id]
	if !ok {
		return domain.ErrNotFound
	}
	prev := f
	f.SHA256 = sha256hex
	r.s.files[room][id] = f
	if err := r.s.saveLocked(); err != nil {
		r.s.files[room][id] = prev
		return err
	}
	return nil
}

// DeviceRepo returns the port.DeviceRepository view of the store.
func (s *Store) DeviceRepo() *DeviceRepo { return &DeviceRepo{s: s} }

// DeviceRepo implements port.DeviceRepository on top of a Store.
// Online presence is never written: a restart always loads devices as offline.
type DeviceRepo struct{ s *Store }

func (r *DeviceRepo) Save(_ context.Context, d domain.Device) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	r.s.devices[d.ID] = d
	return r.s.saveLocked()
}

func (r *DeviceRepo) Get(_ context.Context, id domain.DeviceID) (domain.Device, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	d, ok := r.s.devices[id]
	if !ok {
		return domain.Device{}, domain.ErrNotFound
	}
	return d, nil
}

func (r *DeviceRepo) ListByRoom(_ context.Context, room domain.RoomID) ([]domain.Device, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	var out []domain.Device
	for _, d := range r.s.devices {
		if d.RoomID == room {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].JoinedAt.Equal(out[j].JoinedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].JoinedAt.Before(out[j].JoinedAt)
	})
	return out, nil
}

func (r *DeviceRepo) Delete(_ context.Context, id domain.DeviceID) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	delete(r.s.devices, id)
	return r.s.saveLocked()
}
