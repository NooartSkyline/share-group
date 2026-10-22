package transfer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"lanshare/internal/domain"
)

const (
	// janitorInterval is how often RunJanitor looks for expired sessions. It
	// is far below the 5 minutes Req 4.2 allows, so a session is removed at
	// most a minute after its TTL ran out.
	janitorInterval = time.Minute

	// legacyTempAge is how old a leftover .upload-* temp file of the one-shot
	// upload must be before Recover removes it: a file younger than this may
	// belong to a server that is still running on the same data directory.
	legacyTempAge = time.Hour

	// shutdownGrace is how long BeginShutdown waits for interrupted appends
	// to make their bytes durable.
	shutdownGrace = 3 * time.Second
)

func (s *Service) janitorDelay() time.Duration {
	if s.upCfg.janitorEvery > 0 {
		return s.upCfg.janitorEvery
	}
	return janitorInterval
}

func (s *Service) shutdownDelay() time.Duration {
	if s.upCfg.shutdownWait > 0 {
		return s.upCfg.shutdownWait
	}
	return shutdownGrace
}

// partialKey identifies a partial file the way its owner session does.
type partialKey struct {
	room domain.RoomID
	id   domain.SessionID
}

// Recover brings the data directory and the in-memory registry back in line
// after a start. Call it once, before the server accepts its first request and
// before RunJanitor; it does nothing until ConfigureUploads has run.
//
// In order:
//  1. A persisted record that cannot be parsed is deleted with a warning.
//  2. Each readable record is judged against its partial file. A record that
//     fails domain.UploadSession.Valid (or whose SHA-256 state cannot be
//     restored), whose partial is missing, or whose partial is shorter than
//     the Confirmed_Offset cannot be resumed: the record and any partial are
//     deleted, and later requests for it get not found. A partial longer than
//     the Confirmed_Offset holds a tail that was never confirmed (the process
//     died mid-append) and is truncated to exactly the offset.
//  3. Every partial that no surviving record owns is deleted.
//  4. Legacy .upload-* temp files older than one hour are deleted.
//  5. The registry, the per-device and per-room counts and the disk ledger are
//     rebuilt from the surviving records, so each partial on disk belongs to
//     exactly one registered session and the other way round.
//
// Corrupt or lost records never make Recover fail. It returns an error only
// when the disk state cannot be made consistent: the stores cannot be read, or
// a partial cannot be truncated. Final file content that has no metadata (the
// process died between promoting a finished partial and saving its metadata)
// cannot be told apart from other content through the ports, so it is never
// deleted; the case is logged as a warning when its record is found without
// a partial. Neither the resume key nor its hash is ever logged.
func (s *Service) Recover(ctx context.Context) error {
	if !s.uploadsConfigured() {
		return nil
	}
	now := s.clock.Now()

	records, corrupt, err := s.upDeps.Sessions.LoadAll(ctx)
	if err != nil {
		return fmt.Errorf("load upload sessions: %w", err)
	}
	for _, p := range corrupt {
		slog.Warn("removing an upload session record that cannot be read", "record", filepath.Base(p))
		if err := s.upDeps.Sessions.DeleteCorrupt(ctx, p); err != nil {
			slog.Warn("cannot delete an unreadable upload session record", "record", filepath.Base(p), "error", err)
		}
	}

	keep := make([]domain.UploadSession, 0, len(records))
	owned := make(map[partialKey]bool, len(records))
	discarded := 0
	for _, rec := range records {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("recover upload sessions: %w", err)
		}
		ok, err := s.checkRecovered(ctx, rec)
		if err != nil {
			return err
		}
		if !ok {
			discarded++
			s.discardRecovered(ctx, rec)
			continue
		}
		keep = append(keep, rec)
		owned[partialKey{rec.RoomID, rec.ID}] = true
	}

	orphans, err := s.deleteOrphanPartials(ctx, owned)
	if err != nil {
		return err
	}

	swept, err := s.upDeps.Partials.SweepLegacyTemp(ctx, now.Add(-legacyTempAge))
	if err != nil {
		// Best effort: a stray temp file wastes disk but harms nothing.
		slog.Warn("cannot remove old temporary upload files", "error", err)
	}

	for _, rec := range keep {
		res := s.upDeps.Guard.Adopt(string(rec.ID), uint64(rec.Declared-rec.Offset))
		if !s.adoptSession(newSessionRT(rec, res)) {
			res.Release() // already registered; keep the first one
		}
	}

	slog.Info("upload sessions recovered",
		"kept", len(keep), "discarded", discarded, "corrupt", len(corrupt),
		"orphanPartials", orphans, "legacyTemp", swept)
	return nil
}

// checkRecovered decides whether a persisted record can be resumed, and cuts
// an unconfirmed tail off its partial. false means the record is to be
// discarded; the error is for failures that leave the disk state unknown.
func (s *Service) checkRecovered(ctx context.Context, rec domain.UploadSession) (bool, error) {
	if err := rec.Valid(); err != nil {
		slog.Warn("discarding an invalid upload session record", "session", rec.ID, "error", err)
		return false, nil
	}
	if _, err := restoreHashState(rec.HashState); err != nil {
		slog.Warn("discarding an upload session whose SHA-256 state is unreadable", "session", rec.ID, "error", err)
		return false, nil
	}

	size, err := s.upDeps.Partials.Size(ctx, rec.RoomID, rec.ID)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		if rec.Offset == rec.Declared {
			// The signature of a crash between promoting the finished
			// partial and saving the file's metadata. The content may be
			// sitting in the room folder; it is left alone on purpose.
			slog.Warn("upload session has no partial file although it was fully received; its content may exist without metadata and is left in place", "session", rec.ID)
		} else {
			slog.Warn("discarding an upload session whose partial file is missing", "session", rec.ID)
		}
		return false, nil
	case err != nil:
		return false, fmt.Errorf("measure partial file of session %s: %w", rec.ID, err)
	case size < rec.Offset:
		slog.Warn("discarding an upload session whose partial file is shorter than its confirmed offset", "session", rec.ID)
		return false, nil
	case size > rec.Offset:
		if err := s.upDeps.Partials.Truncate(ctx, rec.RoomID, rec.ID, rec.Offset); err != nil {
			return false, fmt.Errorf("cut unconfirmed tail of the partial file of session %s: %w", rec.ID, err)
		}
	}
	return true, nil
}

// discardRecovered deletes the record and the partial (if any) of a session
// that cannot be resumed. Failures are logged: whatever is left is not owned
// by a registered session and the next start removes it.
func (s *Service) discardRecovered(ctx context.Context, rec domain.UploadSession) {
	if err := s.upDeps.Partials.Delete(ctx, rec.RoomID, rec.ID); err != nil {
		slog.Warn("cannot delete the partial file of a discarded upload session", "session", rec.ID, "error", err)
	}
	if err := s.upDeps.Sessions.Delete(ctx, rec.RoomID, rec.ID); err != nil {
		slog.Warn("cannot delete the record of a discarded upload session", "session", rec.ID, "error", err)
	}
}

// deleteOrphanPartials deletes every partial file not owned by a surviving
// record and returns how many it removed.
func (s *Service) deleteOrphanPartials(ctx context.Context, owned map[partialKey]bool) (int, error) {
	refs, err := s.upDeps.Partials.List(ctx)
	if err != nil {
		return 0, fmt.Errorf("list partial files: %w", err)
	}
	removed := 0
	for _, ref := range refs {
		if err := ctx.Err(); err != nil {
			return removed, fmt.Errorf("remove orphan partial files: %w", err)
		}
		if owned[partialKey{ref.Room, ref.ID}] {
			continue
		}
		if err := s.upDeps.Partials.Delete(ctx, ref.Room, ref.ID); err != nil {
			slog.Warn("cannot delete an orphan partial file", "session", ref.ID, "error", err)
			continue
		}
		removed++
	}
	return removed, nil
}

// adoptSession puts a recovered session in the registry without the
// per-device and per-room limits: the session was admitted before the restart
// and is not refused now. It reports false when the id is already registered.
func (s *Service) adoptSession(rt *sessionRT) bool {
	s.regMu.Lock()
	defer s.regMu.Unlock()
	if _, dup := s.sessions[rt.id]; dup {
		return false
	}
	s.sessions[rt.id] = rt
	s.perDevice[rt.creator]++
	s.perRoom[rt.room]++
	return true
}

// RunJanitor removes expired sessions every minute until ctx is cancelled.
// Run it in its own goroutine after Recover. It does nothing until
// ConfigureUploads has run.
//
// A session is expired once the clock reaches its ExpiresAt(TTL), that is TTL
// after its last append that wrote a byte (or after its creation). Each run
// also drops Completion_Records older than CompletionKeep.
func (s *Service) RunJanitor(ctx context.Context) {
	if !s.uploadsConfigured() {
		return
	}
	tick := time.NewTicker(s.janitorDelay())
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			s.sweep(ctx)
		}
	}
}

// sweep is one janitor run.
func (s *Service) sweep(ctx context.Context) {
	for _, rt := range s.snapshotSessions() {
		s.expire(ctx, rt)
	}
	s.dropExpiredCompletions()
	s.sweepExpiredFiles(ctx)
}

// expire removes rt if it has outlived its TTL and nothing is using it.
//
// The decision and the switch to dead are made under rt.mu, so an append that
// arrives at that moment either was admitted first (then the session has an
// active append and is not expired) or finds the session gone. A session being
// completed is left to the complete. If the room was ever told this session
// was running or interrupted, it now hears "stopped" once, with the bytes the
// server still held; a session the room never heard of (for instance one
// recovered after a restart and not resumed) produces no event.
func (s *Service) expire(ctx context.Context, rt *sessionRT) {
	rt.mu.Lock()
	if rt.dead || rt.completing != nil || !s.expiredLocked(rt) {
		rt.mu.Unlock()
		return
	}
	rt.dead = true
	confirmed := rt.rec.Offset
	rt.mu.Unlock()

	// completeTeardown unregisters, releases the reservation and the activity
	// hold, and deletes the partial and the record; it hands back the tracker.
	tr := s.completeTeardown(context.WithoutCancel(ctx), rt, true)
	if tr.wasAnnounced() {
		tr.rewind(confirmed)
		tr.finish(StateStopped)
	}
	slog.Info("expired upload session removed", "session", rt.id)
}

// BeginShutdown makes every append in flight stop reading and record the bytes
// it has, then waits up to three seconds (or until ctx is done) for them all
// to be durable. Call it before shutting the HTTP server down, so a restart
// finds each session at exactly the offset the client was last told. It does
// nothing until ConfigureUploads has run.
//
// It does not refuse new appends; the HTTP server stops accepting requests
// right after, and an append that sneaks in is cut by the server shutdown and
// recovered at the next start like any crash.
func (s *Service) BeginShutdown(ctx context.Context) {
	if !s.uploadsConfigured() {
		return
	}
	var runs []*appendRun
	for _, rt := range s.snapshotSessions() {
		rt.mu.Lock()
		if rt.active != nil {
			runs = append(runs, rt.active)
		}
		rt.mu.Unlock()
	}
	for _, r := range runs {
		r.interrupt()
	}

	timer := time.NewTimer(s.shutdownDelay())
	defer timer.Stop()
	for _, r := range runs {
		select {
		case <-r.done:
		case <-timer.C:
			slog.Warn("upload appends did not finish recording before shutdown")
			return
		case <-ctx.Done():
			return
		}
	}
}
