// Package peerlog remembers which client addresses reached the server lately.
//
// Invariants: it holds at most MaxEntries addresses, none older than Retention
// relative to the injected clock, and it is safe for concurrent use. Memory is
// therefore bounded no matter how many distinct clients connect.
package peerlog

import (
	"net/netip"
	"sort"
	"sync"
	"time"

	"lanshare/internal/usecase/port"
)

const (
	// MaxEntries is the most distinct addresses kept at once.
	MaxEntries = 256
	// Retention is how long an address is remembered after it was last seen.
	Retention = 10 * time.Minute
)

// Log implements port.PeerLog.
type Log struct {
	clock port.Clock

	mu   sync.Mutex
	seen map[netip.Addr]time.Time
}

var _ port.PeerLog = (*Log)(nil)

// New returns an empty Log that ages entries against clock.
func New(clock port.Clock) *Log {
	return &Log{clock: clock, seen: make(map[netip.Addr]time.Time)}
}

// Record notes that ip was seen at the given time. It keeps the latest time
// per address. If the log is full, the least recently seen address is dropped
// to make room.
func (l *Log) Record(ip netip.Addr, at time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.prune()

	if at.Before(l.clock.Now().Add(-Retention)) {
		return // already expired
	}
	if prev, ok := l.seen[ip]; ok {
		if at.After(prev) {
			l.seen[ip] = at
		}
		return
	}
	if len(l.seen) >= MaxEntries {
		l.evictOldest()
	}
	l.seen[ip] = at
}

// Since returns the addresses last seen at or after t, ordered by address.
func (l *Log) Since(t time.Time) []port.PeerSeen {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.prune()

	var out []port.PeerSeen
	for ip, at := range l.seen {
		if !at.Before(t) {
			out = append(out, port.PeerSeen{Addr: ip, LastSeen: at})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Addr.Less(out[j].Addr) })
	return out
}

// prune drops entries older than Retention. The caller holds l.mu.
func (l *Log) prune() {
	cutoff := l.clock.Now().Add(-Retention)
	for ip, at := range l.seen {
		if at.Before(cutoff) {
			delete(l.seen, ip)
		}
	}
}

// evictOldest removes the least recently seen address. The caller holds l.mu.
func (l *Log) evictOldest() {
	var (
		oldest netip.Addr
		oldAt  time.Time
		found  bool
	)
	for ip, at := range l.seen {
		if !found || at.Before(oldAt) {
			oldest, oldAt, found = ip, at, true
		}
	}
	if found {
		delete(l.seen, oldest)
	}
}
