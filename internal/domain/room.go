// Package domain holds the core entities and business rules of LAN Share.
// It must not import HTTP, SQL, filesystem or any other infrastructure package.
package domain

import (
	"crypto/rand"
	"crypto/subtle"
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
