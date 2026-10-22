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
	"strings"
	"syscall"
	"time"

	"lanshare/internal/domain"
	"lanshare/internal/usecase/port"
)

var safeName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// putBufSize is the copy window for a one-shot Put. Append requests stay at
// 1 MiB (Req 2.10); this path is a single stream into a temp file.
const putBufSize = 4 << 20

// Store implements port.FileStore and port.ThumbStore; Partials() exposes
// port.PartialStore. Partial uploads live under partial/<room>/<id>.part, on the same drive as
// rooms/ so promoting one to a real file is a rename and never a copy.
type Store struct {
	root string
}

// New returns a store rooted at dir (created on first write).
func New(dir string) *Store { return &Store{root: dir} }

var (
	_ port.FileStore    = (*Store)(nil)
	_ port.ThumbStore   = (*Store)(nil)
	_ port.PartialStore = (*Partials)(nil)
)

// mapDiskFull turns "no space left" and "quota exceeded" into
// domain.ErrDiskFull so use cases never see syscall errors. Other errors pass
// through unchanged.
func mapDiskFull(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, syscall.ENOSPC) || errors.Is(err, syscall.EDQUOT) {
		return fmt.Errorf("%w: %w", domain.ErrDiskFull, err)
	}
	return err
}

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
		return 0, mapDiskFull(err)
	}
	tmp, err := os.CreateTemp(dir, ".upload-*")
	if err != nil {
		return 0, mapDiskFull(err)
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			tmp.Close()
			os.Remove(tmpName)
		}
	}()

	n, err := io.CopyBuffer(tmp, r, make([]byte, putBufSize))
	if err != nil {
		return 0, mapDiskFull(err)
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if err := tmp.Chmod(0o600); err != nil {
		return 0, err
	}
	if err := tmp.Close(); err != nil {
		return 0, mapDiskFull(err)
	}
	if err := os.Rename(tmpName, final); err != nil {
		return 0, mapDiskFull(err)
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

func (s *Store) RemoveRoom(_ context.Context, room domain.RoomID) error {
	if !safeName.MatchString(string(room)) {
		return fmt.Errorf("%w: room id", domain.ErrInvalidInput)
	}
	for _, p := range []string{
		filepath.Join(s.root, "rooms", string(room)),
		filepath.Join(s.root, "thumbs", string(room)),
		filepath.Join(s.root, partialDir, string(room)),
	} {
		if err := os.RemoveAll(p); err != nil {
			return err
		}
	}
	return nil
}

// ---- partial uploads (port.PartialStore) ----

// Partials implements port.PartialStore on top of a Store. It is a separate
// type because PartialStore.Delete takes a session id while FileStore.Delete
// takes a file id, and Go has no overloading.
type Partials struct{ store *Store }

// Partials returns the partial-upload view of the store.
func (s *Store) Partials() *Partials { return &Partials{store: s} }

const (
	partialDir    = "partial"
	partialSuffix = ".part"
	legacyPrefix  = ".upload-"
)

func (s *Store) partialRoomDir(room domain.RoomID) (string, error) {
	if !safeName.MatchString(string(room)) {
		return "", fmt.Errorf("%w: room id", domain.ErrInvalidInput)
	}
	return filepath.Join(s.root, partialDir, string(room)), nil
}

func (s *Store) partialPath(room domain.RoomID, id domain.SessionID) (string, error) {
	d, err := s.partialRoomDir(room)
	if err != nil {
		return "", err
	}
	if !safeName.MatchString(string(id)) {
		return "", fmt.Errorf("%w: session id", domain.ErrInvalidInput)
	}
	return filepath.Join(d, string(id)+partialSuffix), nil
}

// notFound maps the fs "does not exist" error to domain.ErrNotFound.
func notFound(err error) error {
	if errors.Is(err, os.ErrNotExist) {
		return domain.ErrNotFound
	}
	return err
}

// partialWriter reports a full disk as domain.ErrDiskFull from Write and Sync,
// so the upload path can stop cleanly and persist the offset it reached.
type partialWriter struct{ f *os.File }

func (w *partialWriter) Write(p []byte) (int, error) {
	n, err := w.f.Write(p)
	return n, mapDiskFull(err)
}
func (w *partialWriter) Sync() error  { return mapDiskFull(w.f.Sync()) }
func (w *partialWriter) Close() error { return w.f.Close() }

func (s *Partials) Create(ctx context.Context, room domain.RoomID, id domain.SessionID) error {
	p, err := s.store.partialPath(room, id)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return mapDiskFull(err)
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return mapDiskFull(err)
	}
	return f.Close()
}

// OpenAppend cuts the partial back to offset first, so bytes written after the
// last persisted offset (a request that died mid-way) can never survive. An
// offset beyond the current length is rejected: extending would invent zeros.
func (s *Partials) OpenAppend(ctx context.Context, room domain.RoomID, id domain.SessionID, offset int64) (port.PartialWriter, error) {
	p, err := s.store.partialPath(room, id)
	if err != nil {
		return nil, err
	}
	if offset < 0 {
		return nil, fmt.Errorf("%w: negative offset", domain.ErrInvalidInput)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(p, os.O_WRONLY, 0o600)
	if err != nil {
		return nil, notFound(mapDiskFull(err))
	}
	ok := false
	defer func() {
		if !ok {
			f.Close()
		}
	}()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if offset > st.Size() {
		return nil, fmt.Errorf("%w: offset %d beyond partial length %d", domain.ErrInvalidInput, offset, st.Size())
	}
	if offset < st.Size() {
		if err := f.Truncate(offset); err != nil {
			return nil, mapDiskFull(err)
		}
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}
	ok = true
	return &partialWriter{f: f}, nil
}

func (s *Partials) Size(_ context.Context, room domain.RoomID, id domain.SessionID) (int64, error) {
	p, err := s.store.partialPath(room, id)
	if err != nil {
		return 0, err
	}
	st, err := os.Stat(p)
	if err != nil {
		return 0, notFound(err)
	}
	return st.Size(), nil
}

// Truncate only shortens; asking for more than the current length is an error
// rather than a silent zero-fill.
func (s *Partials) Truncate(_ context.Context, room domain.RoomID, id domain.SessionID, size int64) error {
	p, err := s.store.partialPath(room, id)
	if err != nil {
		return err
	}
	if size < 0 {
		return fmt.Errorf("%w: negative size", domain.ErrInvalidInput)
	}
	f, err := os.OpenFile(p, os.O_WRONLY, 0o600)
	if err != nil {
		return notFound(err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if size > st.Size() {
		return fmt.Errorf("%w: size %d beyond partial length %d", domain.ErrInvalidInput, size, st.Size())
	}
	if size == st.Size() {
		return nil
	}
	return mapDiskFull(f.Truncate(size))
}

func (s *Partials) Delete(_ context.Context, room domain.RoomID, id domain.SessionID) error {
	p, err := s.store.partialPath(room, id)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (s *Partials) Promote(_ context.Context, room domain.RoomID, id domain.SessionID, file domain.FileID) error {
	from, err := s.store.partialPath(room, id)
	if err != nil {
		return err
	}
	to, err := s.store.path(room, file)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
		return mapDiskFull(err)
	}
	if err := os.Rename(from, to); err != nil {
		return notFound(mapDiskFull(err))
	}
	return nil
}

func (s *Partials) Demote(_ context.Context, room domain.RoomID, file domain.FileID, id domain.SessionID) error {
	from, err := s.store.path(room, file)
	if err != nil {
		return err
	}
	to, err := s.store.partialPath(room, id)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
		return mapDiskFull(err)
	}
	if err := os.Rename(from, to); err != nil {
		return notFound(mapDiskFull(err))
	}
	return nil
}

// List walks partial/<room>/*.part. Entries whose names do not look like ours
// are skipped, never deleted: the janitor only touches what it can identify.
func (s *Partials) List(ctx context.Context) ([]port.PartialRef, error) {
	rooms, err := os.ReadDir(filepath.Join(s.store.root, partialDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []port.PartialRef
	for _, r := range rooms {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !r.IsDir() || !safeName.MatchString(r.Name()) {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(s.store.root, partialDir, r.Name()))
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			id, ok := strings.CutSuffix(e.Name(), partialSuffix)
			if !ok || e.IsDir() || !safeName.MatchString(id) {
				continue
			}
			info, err := e.Info()
			if errors.Is(err, os.ErrNotExist) {
				continue // removed while we listed
			}
			if err != nil {
				return nil, err
			}
			out = append(out, port.PartialRef{Room: domain.RoomID(r.Name()), ID: domain.SessionID(id), Size: info.Size()})
		}
	}
	return out, nil
}

// SweepLegacyTemp removes rooms/*/.upload-* files left by the pre-resumable
// upload path, but only those older than before so an upload in progress right
// now is never touched.
func (s *Partials) SweepLegacyTemp(ctx context.Context, before time.Time) (int, error) {
	rooms, err := os.ReadDir(filepath.Join(s.store.root, "rooms"))
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, r := range rooms {
		if err := ctx.Err(); err != nil {
			return removed, err
		}
		if !r.IsDir() {
			continue
		}
		dir := filepath.Join(s.store.root, "rooms", r.Name())
		entries, err := os.ReadDir(dir)
		if err != nil {
			return removed, err
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasPrefix(e.Name(), legacyPrefix) {
				continue
			}
			info, err := e.Info()
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				return removed, err
			}
			if !info.ModTime().Before(before) {
				continue
			}
			if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				return removed, err
			}
			removed++
		}
	}
	return removed, nil
}
