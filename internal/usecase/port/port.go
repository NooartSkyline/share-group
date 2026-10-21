// Package port declares the interfaces the use cases depend on.
// Adapters and infrastructure implement them; use cases never import adapters.
package port

import (
	"context"
	"io"
	"time"

	"lanshare/internal/domain"
)

type RoomRepository interface {
	Get(ctx context.Context, id domain.RoomID) (domain.Room, error)
	List(ctx context.Context) ([]domain.Room, error)
	Save(ctx context.Context, room domain.Room) error
}

type DeviceRepository interface {
	Save(ctx context.Context, d domain.Device) error
	Get(ctx context.Context, id domain.DeviceID) (domain.Device, error)
	ListByRoom(ctx context.Context, room domain.RoomID) ([]domain.Device, error)
}

// FileRepository stores file metadata. Every lookup is scoped by room so one
// room can never reach another room's files.
type FileRepository interface {
	Save(ctx context.Context, f domain.File) error
	Get(ctx context.Context, room domain.RoomID, id domain.FileID) (domain.File, error)
	ListByRoom(ctx context.Context, room domain.RoomID) ([]domain.File, error)
	Delete(ctx context.Context, room domain.RoomID, id domain.FileID) error
}

// ReadSeekCloser is what a download needs: ranges require Seek.
type ReadSeekCloser interface {
	io.ReadSeeker
	io.Closer
}

// FileStore keeps file contents. It works on streams so large files never
// have to fit in memory.
type FileStore interface {
	// Put writes r completely or not at all and returns the bytes written.
	Put(ctx context.Context, room domain.RoomID, id domain.FileID, r io.Reader) (int64, error)
	Open(ctx context.Context, room domain.RoomID, id domain.FileID) (ReadSeekCloser, error)
	Delete(ctx context.Context, room domain.RoomID, id domain.FileID) error
}

// TokenService issues and resolves session tokens.
type TokenService interface {
	Issue(ctx context.Context, id domain.DeviceID) (string, error)
	Resolve(ctx context.Context, token string) (domain.DeviceID, error)
}

// AttemptLimiter throttles repeated PIN failures per key (e.g. client IP + room).
type AttemptLimiter interface {
	// Check returns how long the key is still locked, or 0 if it may try.
	Check(key string) time.Duration
	// Fail records a failure and returns the attempts left and any new lockout.
	Fail(key string) (remaining int, lockedFor time.Duration)
	// Reset clears the key after a success.
	Reset(key string)
}

// Event is pushed to every connected device of a room.
type Event struct {
	Type   string
	RoomID domain.RoomID
	Data   any
}

const (
	EventDevices     = "devices"
	EventFileAdded   = "file.added"
	EventFileDeleted = "file.deleted"
	EventTransfer    = "transfer"
	EventMessage     = "message"
	EventClipboard   = "clipboard"
)

type EventPublisher interface {
	Publish(e Event)
}

// ThumbStore caches rendered thumbnails next to the files they belong to.
type ThumbStore interface {
	// GetThumb returns domain.ErrNotFound when no thumbnail was stored yet.
	GetThumb(ctx context.Context, room domain.RoomID, id domain.FileID) ([]byte, error)
	PutThumb(ctx context.Context, room domain.RoomID, id domain.FileID, jpeg []byte) error
	DeleteThumb(ctx context.Context, room domain.RoomID, id domain.FileID) error
}

// ThumbRenderer turns an image stream into a JPEG whose longest side is at
// most maxDim. It returns domain.ErrUnsupported for content it cannot or
// should not decode (corrupt, unknown format, absurdly large).
type ThumbRenderer interface {
	Render(r io.Reader, maxDim int) ([]byte, error)
}

// MessageRepository keeps recent chat messages and the shared clipboard.
type MessageRepository interface {
	AppendMessage(ctx context.Context, m domain.Message) error
	RecentMessages(ctx context.Context, room domain.RoomID, limit int) ([]domain.Message, error)
	// GetClip returns a zero Clip when nothing was shared yet.
	GetClip(ctx context.Context, room domain.RoomID) (domain.Clip, error)
	SetClip(ctx context.Context, room domain.RoomID, c domain.Clip) error
}

type Clock interface {
	Now() time.Time
}

type IDGenerator interface {
	NewID() string
}

type NetworkProvider interface {
	Interfaces(ctx context.Context) ([]domain.NetInterface, error)
}

type QRGenerator interface {
	PNG(text string, size int) ([]byte, error)
}
