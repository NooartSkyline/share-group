package domain

import (
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// DeviceID identifies a device that joined a room.
type DeviceID string

// Device is a browser (or the owner's Mac) participating in one room.
type Device struct {
	ID         DeviceID
	RoomID     RoomID
	Name       string
	Platform   string
	Role       Role
	RemoteAddr string
	JoinedAt   time.Time
	Online     bool
}

const (
	maxDeviceNameRunes = 40
	maxPlatformRunes   = 20
	defaultDeviceName  = "Unknown device"
)

// NormalizeDeviceName trims, strips control characters and limits the length
// of a user supplied device name.
func NormalizeDeviceName(name string) string {
	name = cleanText(name, maxDeviceNameRunes)
	if name == "" {
		return defaultDeviceName
	}
	return name
}

// NormalizePlatform cleans the platform label sent by a client.
func NormalizePlatform(p string) string {
	p = cleanText(p, maxPlatformRunes)
	if p == "" {
		return "Web"
	}
	return p
}

func cleanText(s string, maxRunes int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > maxRunes {
		s = string([]rune(s)[:maxRunes])
	}
	return s
}
