package domain

import (
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	MaxMessageRunes   = 4000
	MaxClipboardRunes = 20000
)

// Message is a short text message sent to everyone in a room.
type Message struct {
	ID         string
	RoomID     RoomID
	DeviceID   DeviceID
	DeviceName string
	Text       string
	At         time.Time
}

// Clip is the room's shared clipboard: the latest text any device put there.
type Clip struct {
	Text       string
	DeviceID   DeviceID
	DeviceName string
	At         time.Time
}

// CleanText trims s, drops control characters other than newline and tab, and
// enforces the length limit. An empty result is an error unless allowEmpty.
func CleanText(s string, maxRunes int, allowEmpty bool) (string, error) {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if unicode.IsControl(r) || r == utf8.RuneError {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if s == "" && !allowEmpty {
		return "", fmt.Errorf("%w: empty text", ErrInvalidInput)
	}
	if utf8.RuneCountInString(s) > maxRunes {
		return "", fmt.Errorf("%w: text longer than %d characters", ErrInvalidInput, maxRunes)
	}
	return s, nil
}
