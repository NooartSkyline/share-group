package diagnostics

import (
	"context"
	"fmt"
	"sync"
	"time"

	"lanshare/internal/domain"
	"lanshare/internal/usecase/port"
)

// Timing defaults. Command timeouts are per external command; Overall is the
// ceiling for one whole run; CacheTTL is how long a finished run is reused.
const (
	DefaultCommandTimeout = 3 * time.Second
	DefaultSlowTimeout    = 10 * time.Second // system_profiler
	DefaultOverall        = 12 * time.Second
	DefaultCacheTTL       = 30 * time.Second

	// reachWindow is how far back a request still counts as "recent".
	reachWindow = 10 * time.Minute
)

// ActiveNetwork names the interface the join URL points at. *network.Service
// satisfies it.
type ActiveNetwork interface {
	Active(ctx context.Context) (domain.NetInterface, error)
}

// PartialCounter reports the bytes held by unfinished uploads.
// *transfer.Service satisfies it.
type PartialCounter interface {
	PartialBytes() int64
}

// PowerReporter reports whether the Mac is being kept awake.
// *power.Guard satisfies it.
type PowerReporter interface {
	Status() domain.PowerStatus
}

// Limits overrides the timing defaults. A zero field keeps its default.
type Limits struct {
	Command  time.Duration // per external command
	Slow     time.Duration // per system_profiler call
	Overall  time.Duration // whole run
	CacheTTL time.Duration
}

// Deps are the collaborators of Service. Every field except Limits and
// ExePath is required; a missing one makes only the checks that use it
// report "unknown".
type Deps struct {
	Clock      port.Clock
	Network    ActiveNetwork
	Interfaces port.NetworkProvider
	Locals     port.LocalAddrProvider
	Peers      port.PeerLog
	Runner     port.CommandRunner
	// Disk and MinFree describe the volume that holds the data directory and
	// the free-space reserve (-min-free) the disk check compares against.
	Disk     port.DiskSpace
	MinFree  uint64
	Partials PartialCounter
	Power    PowerReporter
	// ExePath is the absolute path of this server binary. It is handed to the
	// firewall tool as a fixed argument; it never comes from a request.
	ExePath string
	Limits  Limits
}

// RunInput are the per-request options of Service.Run.
type RunInput struct {
	// Fresh skips the cache and asks for a new run.
	Fresh bool
	// Secure says the request arrived over TLS. It is read per request and
	// never cached, because the same report can be asked for over HTTP and
	// HTTPS.
	Secure bool
}

// Report is the result of one diagnostics run.
type Report struct {
	// GeneratedAt is when the run that produced the checks finished.
	GeneratedAt time.Time
	// Cached is true when the checks come from an earlier run.
	Cached bool
	// Checks holds exactly one result per check, in a fixed order.
	Checks []domain.CheckResult
}

// Service runs the environment checks for the Owner.
//
// Invariants: only an Owner can run it (ErrForbidden is returned before
// anything else happens); every report holds exactly seven results; one
// failing check never changes the others; no result contains raw command
// output, a Wi-Fi name, a PIN, a token, a resume key or a file name, since
// Detail and Hint are built only from values parsed by this package.
type Service struct {
	deps   Deps
	limits Limits
	checks []checkDef

	mu     sync.Mutex
	cache  *Report // last finished run; guarded by mu
	flight *flight // run in progress, shared by concurrent callers; guarded by mu
}

// flight is one shared run. report is written before done is closed.
type flight struct {
	done   chan struct{}
	report Report
}

// NewService builds the service from explicit dependencies.
func NewService(d Deps) *Service {
	lim := d.Limits
	if lim.Command <= 0 {
		lim.Command = DefaultCommandTimeout
	}
	if lim.Slow <= 0 {
		lim.Slow = DefaultSlowTimeout
	}
	if lim.Overall <= 0 {
		lim.Overall = DefaultOverall
	}
	if lim.CacheTTL <= 0 {
		lim.CacheTTL = DefaultCacheTTL
	}
	s := &Service{deps: d, limits: lim}
	s.checks = s.definitions()
	return s
}

// Run returns the diagnostics report for an Owner.
//
// A non-Owner gets domain.ErrForbidden and nothing is run. Unless in.Fresh is
// set, a report finished less than 30 seconds ago (by the injected Clock) is
// returned with Cached=true. Callers that arrive while a run is in progress
// share it instead of starting another, so system_profiler never runs twice
// at once; this also applies to a Fresh caller, who then gets the run that is
// already under way.
//
// The run itself does not depend on ctx: it is bounded by the 12 second
// ceiling, and a caller that gives up only stops waiting for it.
func (s *Service) Run(ctx context.Context, actor domain.Device, in RunInput) (Report, error) {
	if !actor.Role.CanManage() {
		return Report{}, domain.ErrForbidden
	}
	if err := ctx.Err(); err != nil {
		return Report{}, fmt.Errorf("diagnostics: run: %w", err)
	}

	now := s.deps.Clock.Now()
	s.mu.Lock()
	if !in.Fresh && s.cache != nil {
		if age := now.Sub(s.cache.GeneratedAt); age >= 0 && age < s.limits.CacheTTL {
			rep := s.cache.clone()
			s.mu.Unlock()
			return withSecure(rep, in.Secure, true), nil
		}
	}
	fl := s.flight
	if fl == nil {
		fl = &flight{done: make(chan struct{})}
		s.flight = fl
		go s.execute(fl, in.Secure)
	}
	s.mu.Unlock()

	select {
	case <-fl.done:
		return withSecure(fl.report.clone(), in.Secure, false), nil
	case <-ctx.Done():
		return Report{}, fmt.Errorf("diagnostics: run: %w", ctx.Err())
	}
}

// execute performs the shared run, publishes it to the cache and wakes the
// callers waiting on fl.
func (s *Service) execute(fl *flight, secure bool) {
	rep := s.collect(secure)
	fl.report = rep

	s.mu.Lock()
	cached := rep.clone()
	s.cache = &cached
	s.flight = nil
	s.mu.Unlock()
	close(fl.done)
}

// collect starts every check at once and waits for all of them, but no longer
// than the overall ceiling. A check still running at the ceiling is reported
// as unknown with reason timeout. The channel holds one slot per check, so a
// late check can always deliver and exit: no goroutine outlives its check.
func (s *Service) collect(secure bool) Report {
	ctx, cancel := context.WithTimeout(context.Background(), s.limits.Overall)
	defer cancel()

	env := runEnv{secure: secure}
	type indexed struct {
		i int
		r domain.CheckResult
	}
	ch := make(chan indexed, len(s.checks))
	for i, def := range s.checks {
		go func() {
			ch <- indexed{i: i, r: s.safeRun(ctx, def, env)}
		}()
	}

	results := make([]domain.CheckResult, len(s.checks))
	have := make([]bool, len(s.checks))
	take := func(x indexed) { results[x.i], have[x.i] = x.r, true }

	for n := 0; n < len(s.checks); {
		select {
		case x := <-ch:
			take(x)
			n++
		case <-ctx.Done():
			// Take whatever finished at the same moment, then give up.
			for drained := false; !drained; {
				select {
				case x := <-ch:
					take(x)
				default:
					drained = true
				}
			}
			n = len(s.checks)
		}
	}
	for i, def := range s.checks {
		if !have[i] {
			results[i] = def.meta.unknown(ReasonTimeout,
				"ตรวจไม่เสร็จภายในเวลาที่กำหนด ("+def.meta.title+")", def.retryHint)
		}
	}
	return Report{GeneratedAt: s.deps.Clock.Now(), Checks: results}
}

// safeRun runs one check and turns a panic into "unknown" for that check
// only. The panic value is not logged: it could carry parsed data.
func (s *Service) safeRun(ctx context.Context, def checkDef, env runEnv) (res domain.CheckResult) {
	defer func() {
		if recover() != nil {
			logUnknown(def.meta.id, ReasonCommandFailed)
			res = def.meta.unknown(ReasonCommandFailed, "การตรวจนี้ทำงานผิดพลาด", def.retryHint)
		}
	}()
	return enforce(def.meta, def.run(ctx, env))
}

func (r Report) clone() Report {
	r.Checks = append([]domain.CheckResult(nil), r.Checks...)
	return r
}

// withSecure replaces the secure_context result with one for this request.
func withSecure(r Report, secure, cached bool) Report {
	r.Cached = cached
	for i := range r.Checks {
		if r.Checks[i].ID == CheckSecureContext {
			r.Checks[i] = secureContextResult(secure)
		}
	}
	return r
}
