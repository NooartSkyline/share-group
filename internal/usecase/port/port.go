// Package port declares the interfaces the use cases depend on.
// Adapters and infrastructure implement them; use cases never import adapters.
package port

import (
	"context"
	"io"
	"net/netip"
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
	// SetChecksum atomically records sha256hex on an existing file without
	// touching its other fields (Integrity stays as it was). It returns
	// domain.ErrNotFound when the file is gone from that room, and never
	// recreates a deleted file.
	SetChecksum(ctx context.Context, room domain.RoomID, id domain.FileID, sha256hex string) error
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

// SystemPreviewer draws a preview picture of formats the in-process decoders
// cannot read (HEIC, camera RAW, PDF, Office, text ...), using the host
// operating system. It is optional: without one those files get an icon.
type SystemPreviewer interface {
	// Preview returns a PNG or JPEG whose longest side is about maxDim, or an
	// error wrapping domain.ErrUnsupported when the system cannot draw one
	// (unknown format, damaged file, too large, tool missing, timeout).
	Preview(ctx context.Context, name string, kind domain.PreviewKind, src io.Reader, maxDim int) ([]byte, error)
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

// UploadSessionRepository persists resumable upload sessions so they survive a
// restart. Per-session operations are scoped by room; only the startup scan
// spans rooms because recovery must see every record on disk.
type UploadSessionRepository interface {
	// Save is atomic and durable: write a temp file, fsync it, rename it over
	// the record, then fsync the directory. After Save returns nil a crash can
	// never expose a half-written or older record.
	Save(ctx context.Context, s domain.UploadSession) error
	// Delete removes the record. Deleting a record that is already gone is not
	// an error, so cleanup paths can be retried.
	Delete(ctx context.Context, room domain.RoomID, id domain.SessionID) error
	// LoadAll returns every record that parses, and the paths of records that
	// do not (corrupt) so the janitor can remove them. One bad record never
	// hides the good ones; err is reserved for failures to read the store.
	LoadAll(ctx context.Context) (ok []domain.UploadSession, corrupt []string, err error)
	// DeleteCorrupt removes a record reported by LoadAll in corrupt. Such a
	// record cannot be addressed by room and ID, because its contents could
	// not be trusted, so it is named by the path LoadAll returned. A record
	// that is already gone is not an error; a path LoadAll did not return
	// is domain.ErrInvalidInput.
	DeleteCorrupt(ctx context.Context, path string) error
}

// PartialRef describes a partial upload file found on disk.
type PartialRef struct {
	Room domain.RoomID
	ID   domain.SessionID
	Size int64
}

// PartialWriter appends to a partial file. Close does not imply Sync, so the
// caller decides when bytes must be durable.
type PartialWriter interface {
	io.Writer
	Sync() error
	Close() error
}

// PartialStore keeps the bytes of unfinished uploads, one file per session
// inside the room's folder. A partial is promoted to a real file only when the
// upload completes, so a half-finished upload is never visible as a file.
type PartialStore interface {
	// Create makes an empty partial. It fails if one already exists.
	Create(ctx context.Context, room domain.RoomID, id domain.SessionID) error
	// OpenAppend truncates the partial to offset bytes and returns a writer
	// positioned there. It returns domain.ErrNotFound when the partial is gone.
	OpenAppend(ctx context.Context, room domain.RoomID, id domain.SessionID, offset int64) (PartialWriter, error)
	// Size returns the current length, or domain.ErrNotFound.
	Size(ctx context.Context, room domain.RoomID, id domain.SessionID) (int64, error)
	// Truncate shortens the partial to size bytes; used to roll back a request
	// so the file never runs ahead of the persisted offset.
	Truncate(ctx context.Context, room domain.RoomID, id domain.SessionID, size int64) error
	// Delete removes the partial; deleting a missing one is not an error.
	Delete(ctx context.Context, room domain.RoomID, id domain.SessionID) error
	// Promote atomically renames the partial to the final file content of file
	// (same drive, so rename never copies).
	Promote(ctx context.Context, room domain.RoomID, id domain.SessionID, file domain.FileID) error
	// Demote reverses Promote; used to roll back when saving metadata fails.
	Demote(ctx context.Context, room domain.RoomID, file domain.FileID, id domain.SessionID) error
	// List returns every partial on disk across rooms, for startup recovery.
	List(ctx context.Context) ([]PartialRef, error)
	// SweepLegacyTemp removes leftover temp files from the old upload path
	// (rooms/*/.upload-*) last modified before the given time and returns how
	// many were removed.
	SweepLegacyTemp(ctx context.Context, before time.Time) (int, error)
}

// DiskSpace reports free space on the drive that holds the data directory.
type DiskSpace interface {
	// Free returns the bytes an unprivileged user can still write
	// (available blocks times block size), not the raw free blocks.
	Free(ctx context.Context) (uint64, error)
}

// SecretGenerator produces unguessable secrets such as resume keys.
type SecretGenerator interface {
	// NewSecret returns 128 random bits, hex encoded. Use IDGenerator for
	// identifiers; those are not secrets.
	NewSecret() string
}

// ActivityTracker tells the power guard whether a transfer is in progress, so
// the Mac stays awake only while there is real work.
type ActivityTracker interface {
	// Begin marks one transfer (an upload request or a download) as active
	// and returns the function that ends it. Every Begin must be paired with
	// exactly one call of end.
	Begin() (end func())
	// Hold keeps the activity alive for ttl after a request ended, covering
	// an unfinished upload session that may resume. Holding the same key
	// again extends it.
	Hold(key string, ttl time.Duration)
	// Release drops a Hold early. Releasing an unknown key is not an error.
	Release(key string)
}

// CaffeinateProcess is a running sleep-prevention process.
type CaffeinateProcess interface {
	// Stop signals the process and waits for it to exit.
	Stop(ctx context.Context) error
	// Exited receives the exit result once and is then closed, so the owner
	// can detect a process that ended on its own.
	Exited() <-chan error
}

// CaffeinateLauncher starts a sleep-prevention process tied to a process ID.
type CaffeinateLauncher interface {
	// Start prevents sleep while the process with the given pid is alive.
	Start(pid int) (CaffeinateProcess, error)
}

// CommandRunner runs an external command for diagnostics.
type CommandRunner interface {
	// Run executes name with args. ctx decides the timeout. It returns stdout
	// only; stderr is discarded so it cannot leak into responses.
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

// PeerSeen is a client address together with the last time it was seen.
type PeerSeen struct {
	Addr     netip.Addr
	LastSeen time.Time
}

// PeerLog remembers which client addresses reached the server recently.
type PeerLog interface {
	// Record notes that ip made a request at the given time.
	Record(ip netip.Addr, at time.Time)
	// Since returns the addresses last seen at or after t.
	Since(t time.Time) []PeerSeen
}

// LocalAddrProvider lists the addresses this machine owns.
type LocalAddrProvider interface {
	// LocalAddrs returns every IPv4 and IPv6 address of every interface.
	LocalAddrs(ctx context.Context) ([]netip.Addr, error)
}
