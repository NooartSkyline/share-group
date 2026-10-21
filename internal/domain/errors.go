package domain

import (
	"errors"
	"fmt"
	"time"
)

var (
	ErrNotFound     = errors.New("not found")
	ErrUnauthorized = errors.New("unauthorized")
	ErrForbidden    = errors.New("forbidden")
	ErrInvalidInput = errors.New("invalid input")
	ErrInvalidPIN   = errors.New("invalid pin")
	ErrLocked       = errors.New("too many attempts")
	ErrNoNetwork    = errors.New("no usable network interface")
	ErrUnsupported  = errors.New("unsupported content")
)

// Sentinels for resumable uploads, checksums and storage. The adapter maps
// them to HTTP statuses; use cases only compare them with errors.Is.
var (
	ErrUploadBusy        = errors.New("upload already in progress")
	ErrStallTimeout      = errors.New("upload stalled")
	ErrTooManyUploads    = errors.New("too many unfinished uploads")
	ErrChecksumBusy      = errors.New("checksum computation busy")
	ErrChecksumFailed    = errors.New("checksum computation failed")
	ErrDiskFull          = errors.New("disk full")
	ErrInterrupted       = errors.New("transfer interrupted")
	ErrOffsetMismatch    = errors.New("offset mismatch")
	ErrIncomplete        = errors.New("upload incomplete")
	ErrSizeExceeded      = errors.New("declared size exceeded")
	ErrInsufficientSpace = errors.New("insufficient storage")
	ErrChecksumMismatch  = errors.New("checksum mismatch")
	ErrPersistFailed     = errors.New("persisting upload state failed")
)

// OffsetMismatchError is returned when an append starts at an offset other
// than the one the server confirmed. The client resumes from Offset.
type OffsetMismatchError struct {
	// Offset is the Confirmed_Offset the server holds.
	Offset int64
}

func (e *OffsetMismatchError) Error() string {
	return fmt.Sprintf("offset mismatch (server is at %d)", e.Offset)
}

func (e *OffsetMismatchError) Unwrap() error { return ErrOffsetMismatch }

// IncompleteError is returned when completing a session that has not received
// all of its declared bytes.
type IncompleteError struct {
	Offset   int64
	Declared int64
}

func (e *IncompleteError) Error() string {
	return fmt.Sprintf("upload incomplete (%d of %d bytes)", e.Offset, e.Declared)
}

func (e *IncompleteError) Unwrap() error { return ErrIncomplete }

// SizeExceededError is returned when a request would push a session past its
// declared size. The request is rolled back as a whole.
type SizeExceededError struct{}

func (e *SizeExceededError) Error() string { return ErrSizeExceeded.Error() }

func (e *SizeExceededError) Unwrap() error { return ErrSizeExceeded }

// InsufficientStorageError is returned when the disk cannot take a file and
// still keep the configured free-space reserve.
type InsufficientStorageError struct {
	// Required is the declared size plus the reserve; Available is what is
	// left after other unfinished uploads are accounted for. Both in bytes.
	Required  uint64
	Available uint64
}

func (e *InsufficientStorageError) Error() string {
	return fmt.Sprintf("insufficient storage (need %d bytes, %d available)", e.Required, e.Available)
}

func (e *InsufficientStorageError) Unwrap() error { return ErrInsufficientSpace }

// ChecksumMismatchError is returned when the sender's SHA-256 differs from the
// one the server computed. Both are lowercase hex.
type ChecksumMismatchError struct {
	Supplied string
	Computed string
}

func (e *ChecksumMismatchError) Error() string { return ErrChecksumMismatch.Error() }

func (e *ChecksumMismatchError) Unwrap() error { return ErrChecksumMismatch }

// PersistFailedError is returned when upload state could not be written to
// disk. Offset is the last offset that is known to be durable, which is what
// the client must resume from.
type PersistFailedError struct {
	Offset int64
	// Err is the underlying cause, if any.
	Err error
}

func (e *PersistFailedError) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("%s (durable offset %d)", ErrPersistFailed.Error(), e.Offset)
	}
	return fmt.Sprintf("%s (durable offset %d): %v", ErrPersistFailed.Error(), e.Offset, e.Err)
}

// Unwrap lets errors.Is match both ErrPersistFailed and the underlying cause.
func (e *PersistFailedError) Unwrap() []error {
	if e.Err == nil {
		return []error{ErrPersistFailed}
	}
	return []error{ErrPersistFailed, e.Err}
}

// InvalidPINError is returned when a wrong PIN is supplied.
type InvalidPINError struct {
	// Remaining is how many attempts are left before the client is locked out.
	Remaining int
}

func (e *InvalidPINError) Error() string {
	return fmt.Sprintf("invalid pin (%d attempts left)", e.Remaining)
}

func (e *InvalidPINError) Unwrap() error { return ErrInvalidPIN }

// LockedError is returned while a client is locked out after repeated failures.
type LockedError struct {
	RetryAfter time.Duration
}

func (e *LockedError) Error() string {
	return fmt.Sprintf("locked for %s", e.RetryAfter.Round(time.Second))
}

func (e *LockedError) Unwrap() error { return ErrLocked }
