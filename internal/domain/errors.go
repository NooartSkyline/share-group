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
