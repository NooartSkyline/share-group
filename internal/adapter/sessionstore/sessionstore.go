// Package sessionstore persists resumable upload sessions, one JSON file per
// session at <data>/sessions/<id>.json.
//
// One file per session keeps the cost of saving an offset proportional to a
// single record and avoids sharing a lock with the room/file metadata store.
// A record holds the hash of the resume key, never the key itself.
//
// Invariant: a session ID is turned into a path only after it matches
// ^[0-9a-f]{32}$, so an ID taken from a URL can never escape the sessions
// directory.
package sessionstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"lanshare/internal/domain"
)

const (
	dirName    = "sessions"
	recordExt  = ".json"
	tmpExt     = ".tmp"
	recordVer  = 1
	maxRecSize = 1 << 20 // a real record is well under 1 KiB; anything larger is not one
)

var idPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// record is the on-disk shape of an upload session. It lives here so the
// domain type stays free of JSON concerns.
type record struct {
	Version       int       `json:"version"`
	ID            string    `json:"id"`
	RoomID        string    `json:"roomId"`
	CreatorID     string    `json:"creatorId"`
	CreatorName   string    `json:"creatorName"`
	Name          string    `json:"name"`
	Dir           string    `json:"dir"`
	Size          int64     `json:"size"`
	Conflict      string    `json:"conflict"`
	Offset        int64     `json:"offset"`
	HashState     []byte    `json:"hashState"` // base64 in JSON
	ResumeKeyHash string    `json:"resumeKeyHash"`
	CreatedAt     time.Time `json:"createdAt"`
	LastWriteAt   time.Time `json:"lastWriteAt"`
}

func toRecord(s domain.UploadSession) record {
	return record{
		Version:       recordVer,
		ID:            string(s.ID),
		RoomID:        string(s.RoomID),
		CreatorID:     string(s.CreatorID),
		CreatorName:   s.CreatorName,
		Name:          s.Name,
		Dir:           s.Dir,
		Size:          s.Declared,
		Conflict:      s.Conflict,
		Offset:        s.Offset,
		HashState:     s.HashState,
		ResumeKeyHash: s.ResumeKeyHash,
		CreatedAt:     s.CreatedAt,
		LastWriteAt:   s.LastWriteAt,
	}
}

func (r record) session() domain.UploadSession {
	return domain.UploadSession{
		ID:            domain.SessionID(r.ID),
		RoomID:        domain.RoomID(r.RoomID),
		CreatorID:     domain.DeviceID(r.CreatorID),
		CreatorName:   r.CreatorName,
		Name:          r.Name,
		Dir:           r.Dir,
		Declared:      r.Size,
		Conflict:      r.Conflict,
		Offset:        r.Offset,
		HashState:     r.HashState,
		ResumeKeyHash: r.ResumeKeyHash,
		CreatedAt:     r.CreatedAt,
		LastWriteAt:   r.LastWriteAt,
	}
}

// Store implements port.UploadSessionRepository.
type Store struct {
	dir string // <data>/sessions, created lazily with mode 0700

	mu sync.Mutex // serialises writers so two Saves never share a .tmp file
}

// New returns a store rooted at the data directory dataDir.
func New(dataDir string) *Store {
	return &Store{dir: filepath.Join(dataDir, dirName)}
}

// path returns the record path for id, or ErrInvalidInput if id is not a
// 32-character lowercase hex string.
func (s *Store) path(id domain.SessionID) (string, error) {
	if !idPattern.MatchString(string(id)) {
		return "", fmt.Errorf("session id %q: %w", string(id), domain.ErrInvalidInput)
	}
	return filepath.Join(s.dir, string(id)+recordExt), nil
}

// Save writes the record atomically: temp file, fsync, rename over the record,
// then fsync of the directory so the rename itself survives a crash.
func (s *Store) Save(ctx context.Context, sess domain.UploadSession) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	final, err := s.path(sess.ID)
	if err != nil {
		return err
	}
	data, err := json.Marshal(toRecord(sess))
	if err != nil {
		return fmt.Errorf("encode upload session: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("create sessions dir: %w", err)
	}
	tmp := final + tmpExt
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("create session temp file: %w", err)
	}
	defer func() {
		if err != nil {
			_ = os.Remove(tmp)
		}
	}()
	if _, err = f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("write session temp file: %w", err)
	}
	if err = f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("sync session temp file: %w", err)
	}
	if err = f.Close(); err != nil {
		return fmt.Errorf("close session temp file: %w", err)
	}
	if err = os.Rename(tmp, final); err != nil {
		return fmt.Errorf("rename session record: %w", err)
	}
	if err = syncDir(s.dir); err != nil {
		return fmt.Errorf("sync sessions dir: %w", err)
	}
	return nil
}

// Delete removes the record. A record that is already gone is not an error.
// Records are stored flat by session ID, so room does not take part in the
// path; the use case has already checked room ownership before calling.
func (s *Store) Delete(ctx context.Context, _ domain.RoomID, id domain.SessionID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p, err := s.path(id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.Remove(p); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("delete session record: %w", err)
	}
	if err := syncDir(s.dir); err != nil {
		return fmt.Errorf("sync sessions dir: %w", err)
	}
	return nil
}

// DeleteCorrupt removes a record that LoadAll reported as corrupt. It accepts
// only a <name>.json file directly inside the sessions directory, so a path
// handed in by mistake can never remove anything else. A record that is
// already gone is not an error.
func (s *Store) DeleteCorrupt(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	clean := filepath.Clean(path)
	if filepath.Dir(clean) != s.dir || !strings.HasSuffix(clean, recordExt) {
		return fmt.Errorf("not a session record path: %w", domain.ErrInvalidInput)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.Remove(clean); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("delete corrupt session record: %w", err)
	}
	if err := syncDir(s.dir); err != nil {
		return fmt.Errorf("sync sessions dir: %w", err)
	}
	return nil
}

// LoadAll reads every <id>.json record. Records that cannot be parsed, have an
// unknown version, disagree with their file name or fail Valid() are reported
// by path in corrupt. Leftover .tmp files and unrelated files are ignored.
func (s *Store) LoadAll(ctx context.Context) (ok []domain.UploadSession, corrupt []string, err error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("read sessions dir: %w", err)
	}
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, recordExt) {
			continue
		}
		p := filepath.Join(s.dir, name)
		sess, rerr := readRecord(p, strings.TrimSuffix(name, recordExt))
		if rerr != nil {
			corrupt = append(corrupt, p)
			continue
		}
		ok = append(ok, sess)
	}
	return ok, corrupt, nil
}

// readRecord loads and validates one record file whose name stem is stem.
func readRecord(path, stem string) (domain.UploadSession, error) {
	f, err := os.Open(path)
	if err != nil {
		return domain.UploadSession{}, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxRecSize+1))
	if err != nil {
		return domain.UploadSession{}, err
	}
	if len(data) > maxRecSize {
		return domain.UploadSession{}, errors.New("record too large")
	}
	var r record
	if err := json.Unmarshal(data, &r); err != nil {
		return domain.UploadSession{}, err
	}
	if r.Version != recordVer {
		return domain.UploadSession{}, fmt.Errorf("unknown record version %d", r.Version)
	}
	if !idPattern.MatchString(r.ID) || r.ID != stem {
		return domain.UploadSession{}, errors.New("record id does not match file name")
	}
	sess := r.session()
	if err := sess.Valid(); err != nil {
		return domain.UploadSession{}, err
	}
	return sess, nil
}

// syncDir fsyncs a directory so a rename or removal inside it is durable.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	if err := d.Sync(); err != nil {
		_ = d.Close()
		return err
	}
	return d.Close()
}
