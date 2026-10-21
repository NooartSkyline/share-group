// Package storage decides whether the disk can take an incoming file.
//
// Guard keeps a ledger of the bytes that accepted uploads still have to write.
// Free space reported by the disk already excludes bytes written so far, so
// only the unwritten remainder of each upload is subtracted from it. The check
// and the ledger entry are made under one lock, so concurrent requests can
// never together be promised more room than the disk has.
package storage

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sync"

	"lanshare/internal/domain"
	"lanshare/internal/usecase/port"
)

// Guard reserves disk space for uploads. Its zero value is not usable; build
// it with NewGuard. It is safe for concurrent use and its mutex is a leaf in
// the lock order: nothing is called while holding it except DiskSpace.Free.
type Guard struct {
	mu      sync.Mutex
	disk    port.DiskSpace
	minFree uint64
	// ledger maps a key (an upload session id, or an ephemeral key for an
	// upload without a session) to the bytes it has yet to write.
	ledger map[string]uint64
	// holders maps the same keys to the Reservation that currently owns the
	// entry, so a superseded or released handle cannot touch a newer one.
	holders map[string]*Reservation
	log     *slog.Logger
}

// Snapshot is a point-in-time reading of the disk for diagnostics.
type Snapshot struct {
	// Free is the raw free space in bytes; meaningful only if FreeKnown.
	Free uint64
	// MinFree is the configured free-space reserve in bytes.
	MinFree uint64
	// FreeKnown is false when the free space could not be read.
	FreeKnown bool
}

// Reservation is the claim of one key on the ledger. Once released (or
// replaced by a newer reservation of the same key) it is inert.
type Reservation struct {
	g    *Guard
	key  string
	done bool // guarded by g.mu
}

// NewGuard returns a Guard that keeps minFree bytes of the disk untouched.
// A nil logger falls back to slog.Default().
func NewGuard(disk port.DiskSpace, minFree uint64, log *slog.Logger) *Guard {
	if log == nil {
		log = slog.Default()
	}
	return &Guard{
		disk:    disk,
		minFree: minFree,
		ledger:  make(map[string]uint64),
		holders: make(map[string]*Reservation),
		log:     log,
	}
}

// Reserve claims declared bytes under key if the disk can hold them on top of
// every other reservation and the free-space reserve. Reserving a key again
// replaces its earlier claim rather than adding to it. When the free space
// cannot be read the request is accepted and a warning is logged.
//
// A rejection is a *domain.InsufficientStorageError with Required set to
// declared plus the reserve and Available to the free space left after the
// other reservations (never negative). Available equal to Required passes.
func (g *Guard) Reserve(ctx context.Context, key string, declared uint64) (*Reservation, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	free, err := g.readFreeLocked(ctx)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("storage: reserve: %w", ctxErr)
		}
	} else {
		avail := subFloor(free, g.othersLocked(key))
		required := satAdd(declared, g.minFree)
		if avail < required {
			return nil, &domain.InsufficientStorageError{Required: required, Available: avail}
		}
	}

	if old := g.holders[key]; old != nil {
		old.done = true
	}
	r := &Reservation{g: g, key: key}
	g.ledger[key] = declared
	g.holders[key] = r
	return r, nil
}

// Adopt records, without any check, that key still has remaining bytes to
// write. It rebuilds the ledger at start-up for uploads that were accepted
// before the restart: they were promised space then, and dropping them
// because the disk is now fuller would lose the client's progress. Adopting a
// key again replaces its earlier claim.
func (g *Guard) Adopt(key string, remaining uint64) *Reservation {
	g.mu.Lock()
	defer g.mu.Unlock()
	if old := g.holders[key]; old != nil {
		old.done = true
	}
	r := &Reservation{g: g, key: key}
	g.ledger[key] = remaining
	g.holders[key] = r
	return r
}

// Update sets the bytes this reservation still has to write. It does nothing
// once the reservation has been released or replaced.
func (r *Reservation) Update(remaining uint64) {
	r.g.mu.Lock()
	defer r.g.mu.Unlock()
	if r.done {
		return
	}
	r.g.ledger[r.key] = remaining
}

// Release gives the reserved bytes back. Calling it again is harmless.
func (r *Reservation) Release() {
	r.g.mu.Lock()
	defer r.g.mu.Unlock()
	if r.done {
		return
	}
	r.done = true
	delete(r.g.ledger, r.key)
	delete(r.g.holders, r.key)
}

// Release drops whatever is reserved under key. An unknown key is a no-op.
func (g *Guard) Release(key string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if r := g.holders[key]; r != nil {
		r.done = true
	}
	delete(g.ledger, key)
	delete(g.holders, key)
}

// CheckFree reads the raw free space of the drive, for uploads whose size is
// not known in advance. A failed read is logged once and returned.
func (g *Guard) CheckFree(ctx context.Context) (uint64, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.readFreeLocked(ctx)
}

// Snapshot reads the disk for diagnostics. A failed read is logged once and
// reported as FreeKnown=false rather than as an error.
func (g *Guard) Snapshot(ctx context.Context) Snapshot {
	g.mu.Lock()
	defer g.mu.Unlock()
	free, err := g.readFreeLocked(ctx)
	if err != nil {
		return Snapshot{MinFree: g.minFree}
	}
	return Snapshot{Free: free, MinFree: g.minFree, FreeKnown: true}
}

// readFreeLocked asks the disk for its free space and logs one warning per
// failed read. The caller holds g.mu.
func (g *Guard) readFreeLocked(ctx context.Context) (uint64, error) {
	free, err := g.disk.Free(ctx)
	if err != nil {
		g.log.Warn("cannot read free disk space", "error", err)
		return 0, fmt.Errorf("storage: read free space: %w", err)
	}
	return free, nil
}

// othersLocked sums the ledger without the entry for key, saturating at
// MaxUint64. The caller holds g.mu.
func (g *Guard) othersLocked(key string) uint64 {
	var sum uint64
	for k, v := range g.ledger {
		if k == key {
			continue
		}
		sum = satAdd(sum, v)
	}
	return sum
}

func satAdd(a, b uint64) uint64 {
	if a > math.MaxUint64-b {
		return math.MaxUint64
	}
	return a + b
}

func subFloor(a, b uint64) uint64 {
	if a < b {
		return 0
	}
	return a - b
}
