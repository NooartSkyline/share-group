package domain

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"time"
)

// SessionID identifies a resumable upload session. It is random and unrelated
// to the resume key, so knowing one never reveals the other.
type SessionID string

// MaxDeclaredSize is the largest file size a session may declare: 2^53-1, the
// biggest integer a JavaScript Number holds exactly, so browsers can report
// and compare offsets without losing precision.
const MaxDeclaredSize = 1<<53 - 1

// Conflict policies for a name that is already taken in the same folder.
const (
	ConflictRename    = "rename"
	ConflictOverwrite = "overwrite"
)

// UploadSession is the persisted state of one upload that can be resumed.
//
// Invariants: 0 <= Offset <= Declared, and Offset equals the length of the
// partial file on disk and the number of bytes already fed into HashState.
// The raw resume key is never stored, only ResumeKeyHash.
type UploadSession struct {
	ID          SessionID
	RoomID      RoomID
	CreatorID   DeviceID
	CreatorName string // shown as the device name in progress events
	Name        string // already passed through SanitizeFileName
	Dir         string // already passed through SanitizeDir
	Declared    int64  // size announced by the sender, 0..MaxDeclaredSize
	Conflict    string // ConflictRename or ConflictOverwrite
	Offset      int64  // Confirmed_Offset: bytes durably written
	// HashState is the marshalled SHA-256 state after the first Offset bytes,
	// so resuming never needs to re-read the partial file.
	HashState     []byte
	ResumeKeyHash string // hex SHA-256 of the resume key
	CreatedAt     time.Time
	// LastWriteAt is when an append last wrote at least one byte; zero means never.
	LastWriteAt time.Time
}

// Valid reports whether the record is internally consistent. A record that
// fails it was corrupted or tampered with and must not be trusted for resuming.
func (s UploadSession) Valid() error {
	if s.Declared < 0 || s.Declared > MaxDeclaredSize {
		return fmt.Errorf("upload session %q: declared size %d outside 0..%d: %w", s.ID, s.Declared, int64(MaxDeclaredSize), ErrInvalidInput)
	}
	if s.Offset < 0 || s.Offset > s.Declared {
		return fmt.Errorf("upload session %q: offset %d outside 0..%d: %w", s.ID, s.Offset, s.Declared, ErrInvalidInput)
	}
	if s.Conflict != ConflictRename && s.Conflict != ConflictOverwrite {
		return fmt.Errorf("upload session %q: unknown conflict policy %q: %w", s.ID, s.Conflict, ErrInvalidInput)
	}
	if SanitizeFileName(s.Name) != s.Name {
		return fmt.Errorf("upload session %q: file name is not sanitized: %w", s.ID, ErrInvalidInput)
	}
	if SanitizeDir(s.Dir) != s.Dir {
		return fmt.Errorf("upload session %q: dir is not sanitized: %w", s.ID, ErrInvalidInput)
	}
	return nil
}

// ExpiresAt is when the session may be discarded for inactivity: ttl after the
// later of its creation and its last write. Failed or empty appends do not
// move it, so only real progress keeps a session alive.
func (s UploadSession) ExpiresAt(ttl time.Duration) time.Time {
	last := s.CreatedAt
	if s.LastWriteAt.After(last) {
		last = s.LastWriteAt
	}
	return last.Add(ttl)
}

// HashResumeKey returns the value stored in place of a resume key: its SHA-256
// as lowercase hex. The key carries 128 random bits, so a plain hash is enough
// to keep a leaked state file from granting access.
func HashResumeKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// CheckResumeKey reports whether key is the resume key of this session. The
// comparison takes the same time whatever the input, and an empty key or an
// empty stored hash never matches.
func (s UploadSession) CheckResumeKey(key string) bool {
	if key == "" || s.ResumeKeyHash == "" {
		return false
	}
	got := HashResumeKey(key)
	return subtle.ConstantTimeCompare([]byte(got), []byte(s.ResumeKeyHash)) == 1
}
