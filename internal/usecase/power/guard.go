// Package power keeps the Mac awake while files are moving.
//
// Guard is a small state machine. It counts active transfers, decides from
// that count and an injected clock whether a sleep-prevention assertion
// (one caffeinate process) should exist, and starts or stops the process
// through port.CaffeinateLauncher. It never interrupts a transfer: a failing
// or vanished process only changes the status.
package power

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"lanshare/internal/domain"
	"lanshare/internal/usecase/port"
)

const (
	// DefaultGrace is how long the assertion outlives the last transfer.
	DefaultGrace = 30 * time.Second

	// tickInterval is how often Run re-evaluates the state, so that a grace
	// period that ran out is noticed within the 1 second the requirement
	// allows.
	tickInterval = 500 * time.Millisecond

	// stopTimeout bounds how long a stop requested by Step may take.
	stopTimeout = time.Second
)

// Guard holds at most one sleep-prevention process.
//
// Invariants (all under mu):
//   - the assertion is held if and only if enabled and (n > 0 or a transfer
//     has ended less than grace ago), where n counts unfinished Begin calls
//     plus Holds that have not expired yet; the exception is a failure, which
//     leaves it unheld until the next 0 -> 1 change of n;
//   - proc is the only process ever running; a new one is started only when
//     proc is nil.
//
// Guard implements port.ActivityTracker.
type Guard struct {
	clock    port.Clock
	launcher port.CaffeinateLauncher
	pid      int
	grace    time.Duration
	enabled  bool

	mu         sync.Mutex
	open       int                  // Begin calls whose end has not run
	holds      map[string]time.Time // key -> expiry
	lastEnd    time.Time            // when n last dropped to 0
	everActive bool                 // some transfer has existed
	proc       port.CaffeinateProcess
	failed     bool // the last start failed or the process died on its own
	shut       bool // Stop was called; no new process may start
}

var _ port.ActivityTracker = (*Guard)(nil)

// NewGuard returns a Guard that binds caffeinate to pid. With enabled false it
// never starts a process and reports domain.PowerDisabled.
func NewGuard(clock port.Clock, launcher port.CaffeinateLauncher, pid int, grace time.Duration, enabled bool) *Guard {
	return &Guard{
		clock:    clock,
		launcher: launcher,
		pid:      pid,
		grace:    grace,
		enabled:  enabled,
		holds:    map[string]time.Time{},
	}
}

// Begin marks one transfer as active and returns the function that ends it.
// Calling end more than once has no further effect.
func (g *Guard) Begin() (end func()) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.clock.Now()
	g.sweep(now)
	g.beginTransfer(now, func() { g.open++ })
	g.step(now)

	done := false
	return func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		if done {
			return
		}
		done = true
		now := g.clock.Now()
		g.sweep(now)
		g.open--
		g.markIfIdle(now)
		g.step(now)
	}
}

// Hold keeps the activity alive until ttl has passed. Holding a key again
// replaces its expiry with the new one.
func (g *Guard) Hold(key string, ttl time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.clock.Now()
	g.sweep(now)
	g.beginTransfer(now, func() { g.holds[key] = now.Add(ttl) })
	g.sweep(now) // a Hold that is already over counts as ended at its expiry
	g.step(now)
}

// Release drops a Hold early. An unknown key is ignored.
func (g *Guard) Release(key string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.clock.Now()
	g.sweep(now)
	if _, ok := g.holds[key]; !ok {
		return
	}
	delete(g.holds, key)
	g.markIfIdle(now)
	g.step(now)
}

// Step applies the rules at time now: it expires Holds, notices a process that
// ended on its own, and starts or stops the process as needed. Begin, end,
// Hold, Release and Run call it; tests may call it directly.
func (g *Guard) Step(now time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.sweep(now)
	g.step(now)
}

// Status reports the current state of the guard.
func (g *Guard) Status() domain.PowerStatus {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.enabled {
		return domain.PowerDisabled
	}
	g.pollExit()
	switch {
	case g.proc != nil:
		return domain.PowerActive
	case g.failed:
		return domain.PowerUnavailable
	default:
		return domain.PowerIdle
	}
}

// Run calls Step every 500 ms until ctx ends. It does not stop the process;
// call Stop for that.
func (g *Guard) Run(ctx context.Context) {
	t := time.NewTicker(tickInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			g.Step(g.clock.Now())
		}
	}
}

// Stop ends the caffeinate process, whatever the number of transfers, and
// keeps the guard from starting another one. It returns once the process has
// exited or ctx ends.
func (g *Guard) Stop(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.shut = true
	if g.proc == nil {
		return nil
	}
	p := g.proc
	g.proc = nil
	if err := p.Stop(ctx); err != nil {
		return fmt.Errorf("stop caffeinate: %w", err)
	}
	return nil
}

// beginTransfer applies add, which increases n, and clears a failure when n
// goes from 0 to 1 so that exactly one new start attempt follows.
func (g *Guard) beginTransfer(now time.Time, add func()) {
	wasIdle := g.count(now) == 0
	add()
	g.everActive = true
	if wasIdle && g.count(now) > 0 {
		g.failed = false
	}
}

// markIfIdle records now as the end of activity when no transfer is left.
func (g *Guard) markIfIdle(now time.Time) {
	if g.count(now) == 0 {
		g.lastEnd = now
	}
}

// count is n at time now.
func (g *Guard) count(now time.Time) int {
	n := g.open
	for _, exp := range g.holds {
		if exp.After(now) {
			n++
		}
	}
	return n
}

// sweep drops Holds that expired at or before now. An expired Hold ended its
// transfer at its expiry time, not when it was noticed.
func (g *Guard) sweep(now time.Time) {
	for k, exp := range g.holds {
		if exp.After(now) {
			continue
		}
		delete(g.holds, k)
		if exp.After(g.lastEnd) {
			g.lastEnd = exp
		}
	}
}

// step starts or stops the process so that it matches the rules.
func (g *Guard) step(now time.Time) {
	g.pollExit()
	if !g.enabled || g.shut {
		return
	}
	want := g.count(now) > 0 || (g.everActive && now.Sub(g.lastEnd) < g.grace)
	switch {
	case want && g.proc == nil && !g.failed:
		g.start()
	case !want && g.proc != nil:
		g.stopProc()
	}
}

// start launches caffeinate. A failure is logged once and sticks until the
// next 0 -> 1 change of n.
func (g *Guard) start() {
	if g.launcher == nil {
		g.fail("start caffeinate", fmt.Errorf("no launcher configured"))
		return
	}
	p, err := g.launcher.Start(g.pid)
	if err != nil {
		g.fail("start caffeinate", err)
		return
	}
	g.proc = p
}

// stopProc ends the process because the grace period is over. The reference
// is dropped even when stopping fails: caffeinate is bound to our pid, and
// waiting for it must not block transfers.
func (g *Guard) stopProc() {
	p := g.proc
	g.proc = nil
	ctx, cancel := context.WithTimeout(context.Background(), stopTimeout)
	defer cancel()
	if err := p.Stop(ctx); err != nil {
		slog.Warn("power: stopping caffeinate failed", "error", err)
	}
}

// pollExit notices a process that ended without being asked to.
func (g *Guard) pollExit() {
	if g.proc == nil {
		return
	}
	select {
	case err := <-g.proc.Exited():
		g.proc = nil
		if err == nil {
			err = fmt.Errorf("process exited")
		}
		g.fail("caffeinate exited unexpectedly", err)
	default:
	}
}

// fail records one failure: one warning, status unavailable, no retry.
func (g *Guard) fail(what string, err error) {
	g.failed = true
	slog.Warn("power: "+what+"; sleep prevention unavailable", "error", err)
}
