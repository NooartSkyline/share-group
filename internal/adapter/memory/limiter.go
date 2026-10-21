package memory

import (
	"sync"
	"time"

	"lanshare/internal/usecase/port"
)

// LimiterPolicy configures PIN attempt throttling.
type LimiterPolicy struct {
	MaxFailures int           // failures allowed inside Window before lockout
	Window      time.Duration // failures older than this are forgotten
	Lockout     time.Duration // how long a key stays locked
}

type attempts struct {
	failures    int
	windowStart time.Time
	lockedUntil time.Time
}

// AttemptLimiter implements port.AttemptLimiter.
type AttemptLimiter struct {
	clock  port.Clock
	policy LimiterPolicy

	mu   sync.Mutex
	keys map[string]*attempts
}

func NewAttemptLimiter(clock port.Clock, policy LimiterPolicy) *AttemptLimiter {
	return &AttemptLimiter{clock: clock, policy: policy, keys: make(map[string]*attempts)}
}

func (l *AttemptLimiter) Check(key string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	a, ok := l.keys[key]
	if !ok {
		return 0
	}
	if left := a.lockedUntil.Sub(l.clock.Now()); left > 0 {
		return left
	}
	return 0
}

func (l *AttemptLimiter) Fail(key string) (int, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clock.Now()
	l.sweep(now)

	a, ok := l.keys[key]
	if !ok || now.Sub(a.windowStart) > l.policy.Window {
		a = &attempts{windowStart: now}
		l.keys[key] = a
	}
	a.failures++
	if a.failures >= l.policy.MaxFailures {
		a.lockedUntil = now.Add(l.policy.Lockout)
		a.failures = 0
		a.windowStart = now
		return 0, l.policy.Lockout
	}
	return l.policy.MaxFailures - a.failures, 0
}

func (l *AttemptLimiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.keys, key)
}

// sweep removes entries that are neither locked nor inside their window.
func (l *AttemptLimiter) sweep(now time.Time) {
	for k, a := range l.keys {
		if now.After(a.lockedUntil) && now.Sub(a.windowStart) > l.policy.Window {
			delete(l.keys, k)
		}
	}
}
