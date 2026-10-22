// Package domain holds the core entities and business rules of LAN Share.
// It must not import HTTP, SQL, filesystem or any other infrastructure package.
package domain

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"io"
	"math/big"
	"time"
)

// PINLength is the number of digits in a room PIN.
const PINLength = 6

// RoomID identifies a room. Every file and device belongs to exactly one room.
type RoomID string

// Room is an isolated space with its own PIN, devices and files.
type Room struct {
	ID        RoomID
	Name      string
	PIN       string
	CreatedAt time.Time
	// QuotaBytes is the maximum total size of files in the room; 0 means no cap.
	QuotaBytes int64
	// FileTTL is how long a published file is kept; 0 means it is never expired.
	FileTTL time.Duration
	// RequireApproval, when true, holds new devices in Pending until the Owner approves them.
	RequireApproval bool
	// GuestKey is the secret path segment of the upload-only guest link; empty means no link.
	GuestKey string
	// GuestUntil is when the guest link stops working; zero means no link.
	GuestUntil time.Time
	// GuestMax is the largest file a guest may send; 0 means no extra cap.
	GuestMax int64
}

const (
	maxRoomNameRunes = 40
	roomIDBytes      = 4 // 8 hex characters
	guestKeyBytes    = 8 // 16 hex characters
)

// ValidRoomID reports whether s is a safe room identifier for URLs and folders.
func ValidRoomID(s string) bool {
	if len(s) < 1 || len(s) > 32 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
			continue
		}
		if c == '-' && i > 0 {
			continue
		}
		return false
	}
	return true
}

// NewRoomID returns 8 lowercase hex characters from rnd.
func NewRoomID(rnd io.Reader) (RoomID, error) {
	if rnd == nil {
		rnd = rand.Reader
	}
	var b [roomIDBytes]byte
	if _, err := io.ReadFull(rnd, b[:]); err != nil {
		return "", err
	}
	return RoomID(hex.EncodeToString(b[:])), nil
}

// NewGuestKey returns 16 lowercase hex characters from rnd.
func NewGuestKey(rnd io.Reader) (string, error) {
	if rnd == nil {
		rnd = rand.Reader
	}
	var b [guestKeyBytes]byte
	if _, err := io.ReadFull(rnd, b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// NormalizeRoomName trims and bounds a room name. Empty input becomes "ห้อง".
func NormalizeRoomName(name string) string {
	name = cleanText(name, maxRoomNameRunes)
	if name == "" {
		return "ห้อง"
	}
	return name
}

// GuestActive reports whether the guest link can still be used at now.
func (r Room) GuestActive(now time.Time) bool {
	return r.GuestKey != "" && !r.GuestUntil.IsZero() && now.Before(r.GuestUntil)
}

// CheckPIN reports whether candidate matches the room PIN, in constant time.
func (r Room) CheckPIN(candidate string) bool {
	if len(candidate) != len(r.PIN) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(candidate), []byte(r.PIN)) == 1
}

// ValidPINFormat reports whether s is exactly PINLength ASCII digits.
func ValidPINFormat(s string) bool {
	if len(s) != PINLength {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// NewPIN returns a uniformly random PINLength-digit PIN read from rnd.
// Pass crypto/rand.Reader in production.
func NewPIN(rnd io.Reader) (string, error) {
	if rnd == nil {
		rnd = rand.Reader
	}
	limit := big.NewInt(1)
	for i := 0; i < PINLength; i++ {
		limit.Mul(limit, big.NewInt(10))
	}
	n, err := rand.Int(rnd, limit)
	if err != nil {
		return "", err
	}
	s := n.String()
	for len(s) < PINLength {
		s = "0" + s
	}
	return s, nil
}

// Role describes what a device may do inside a room.
type Role string

const (
	RoleOwner    Role = "owner"
	RoleMember   Role = "member"
	RoleUploader Role = "uploader" // upload only (guest links, later phase)
	RoleViewer   Role = "viewer"   // download only
)

// Valid reports whether r is a known role.
func (r Role) Valid() bool {
	switch r {
	case RoleOwner, RoleMember, RoleUploader, RoleViewer:
		return true
	}
	return false
}

// CanUpload reports whether the role may add files to the room.
func (r Role) CanUpload() bool {
	return r == RoleOwner || r == RoleMember || r == RoleUploader
}

// CanDownload reports whether the role may list and download files.
func (r Role) CanDownload() bool {
	return r == RoleOwner || r == RoleMember || r == RoleViewer
}

// CanChat reports whether the role may post messages and use the shared clipboard.
func (r Role) CanChat() bool {
	return r == RoleOwner || r == RoleMember
}

// CanManage reports whether the role may administer the room.
func (r Role) CanManage() bool {
	return r == RoleOwner
}
