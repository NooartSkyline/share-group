// Package filestore keeps file contents on disk, one directory per room.
package filestore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"lanshare/internal/domain"
	"lanshare/internal/usecase/port"
)

var safeName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// Store implements port.FileStore.
type Store struct {
	root string
}

// New returns a store rooted at dir (created on first write).
func New(dir string) *Store { return &Store{root: dir} }

func (s *Store) dir(room domain.RoomID) (string, error) {
	if !safeName.MatchString(string(room)) {
		return "", fmt.Errorf("%w: room id", domain.ErrInvalidInput)
	}
	return filepath.Join(s.root, "rooms", string(room)), nil
}

func (s *Store) path(room domain.RoomID, id domain.FileID) (string, error) {
	d, err := s.dir(room)
	if err != nil {
		return "", err
	}
	if !safeName.MatchString(string(id)) {
		return "", fmt.Errorf("%w: file id", domain.ErrInvalidInput)
	}
	return filepath.Join(d, string(id)), nil
}

// Put streams r into a temporary file and renames it into place only after
// every byte was written, so a dropped connection never leaves a partial file.
func (s *Store) Put(ctx context.Context, room domain.RoomID, id domain.FileID, r io.Reader) (int64, error) {
	final, err := s.path(room, id)
	if err != nil {
		return 0, err
	}
	dir := filepath.Dir(final)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return 0, err
	}
	tmp, err := os.CreateTemp(dir, ".upload-*")
	if err != nil {
		return 0, err
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			tmp.Close()
			os.Remove(tmpName)
		}
	}()

	n, err := io.CopyBuffer(tmp, r, make([]byte, 1<<20))
	if err != nil {
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if err := tmp.Chmod(0o600); err != nil {
		return 0, err
	}
	if err := tmp.Close(); err != nil {
		return 0, err
	}
	if err := os.Rename(tmpName, final); err != nil {
		return 0, err
	}
	committed = true
	return n, nil
}

func (s *Store) Open(_ context.Context, room domain.RoomID, id domain.FileID) (port.ReadSeekCloser, error) {
	p, err := s.path(room, id)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return f, nil
}

// ---- thumbnails (port.ThumbStore) ----

func (s *Store) thumbPath(room domain.RoomID, id domain.FileID) (string, error) {
	if !safeName.MatchString(string(room)) || !safeName.MatchString(string(id)) {
		return "", fmt.Errorf("%w: id", domain.ErrInvalidInput)
	}
	return filepath.Join(s.root, "thumbs", string(room), string(id)+".jpg"), nil
}

func (s *Store) GetThumb(_ context.Context, room domain.RoomID, id domain.FileID) ([]byte, error) {
	p, err := s.thumbPath(room, id)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, domain.ErrNotFound
	}
	return b, err
}

func (s *Store) PutThumb(_ context.Context, room domain.RoomID, id domain.FileID, data []byte) error {
	p, err := s.thumbPath(room, id)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".thumb-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
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
	return os.Rename(tmp.Name(), p)
}

func (s *Store) DeleteThumb(_ context.Context, room domain.RoomID, id domain.FileID) error {
	p, err := s.thumbPath(room, id)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (s *Store) Delete(_ context.Context, room domain.RoomID, id domain.FileID) error {
	p, err := s.path(room, id)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
