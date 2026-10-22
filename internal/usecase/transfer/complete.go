package transfer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"lanshare/internal/domain"
)

// CompleteResult is the outcome of CompleteSession.
type CompleteResult struct {
	File domain.File
	// Repeated is true when the file was published by an earlier (or a
	// concurrent) complete of the same session; nothing was stored or
	// announced by this call.
	Repeated bool
}

// CompleteSession turns a fully received session into a file of the room.
//
// Order of the checks, each failure leaving the session as it was unless
// stated: role (ErrForbidden); a recent completion of this session by a holder
// of the key (the same file again, Repeated); session and key (ErrNotFound,
// the one answer for every reason); the checksum format
// (domain.ErrInvalidInput); Offset below the Declared size
// (*domain.IncompleteError).
//
// Then the session is marked completing, which makes appends answer
// ErrUploadBusy; an append still running is interrupted and awaited (else
// ErrUploadBusy). Two concurrent completes do not race: the second waits for
// the first and returns its file, or takes over if the first failed.
//
// The SHA-256 the server computed is compared with checksum before anything
// is moved: on a mismatch the partial, the session and its reservation are
// deleted, the transfer is reported failed and the error is
// *domain.ChecksumMismatchError. Otherwise, inside the room lock, the name is
// resolved, an overwrite is permitted again for actor (the device that sent
// complete, which need not be the creator), the partial is promoted and the
// metadata saved. A failure there puts the partial back and reports the error,
// publishing nothing; an overwritten file is untouched.
//
// On success the session record, registry entry and reservation are gone
// before this returns, only a Completion_Record (for CompletionKeep) remains,
// the file's uploader is actor, Integrity is verified only when checksum was
// supplied (and so equal), and the room gets one file.added and one transfer
// done. Once the client has been admitted the work finishes even if ctx is
// cancelled, so a lost response can be repeated. The resume key is never
// logged.
func (s *Service) CompleteSession(ctx context.Context, actor domain.Device, id domain.SessionID, key string, checksum *string) (CompleteResult, error) {
	if !actor.CanUpload() {
		return CompleteResult{}, domain.ErrForbidden
	}
	if !s.uploadsConfigured() {
		return CompleteResult{}, domain.ErrNotFound
	}
	if f, ok := s.completedFile(actor.RoomID, id, key); ok {
		return CompleteResult{File: f, Repeated: true}, nil
	}

	var (
		supplied string
		parsed   bool
	)
	for {
		rt, err := s.lookupSession(actor.RoomID, id, key)
		if err == nil {
			if !parsed {
				if checksum != nil {
					if supplied, err = domain.NormalizeChecksum(*checksum); err != nil {
						return CompleteResult{}, fmt.Errorf("checksum: %w", err)
					}
				}
				parsed = true
			}
			var cl completeClaim
			if cl, err = s.claimComplete(rt); err == nil {
				if cl.other != nil {
					// Someone else is completing this session: share the outcome.
					select {
					case <-cl.other.done:
					case <-ctx.Done():
						return CompleteResult{}, fmt.Errorf("wait for complete: %w", ctx.Err())
					}
					if cl.other.err == nil {
						return CompleteResult{File: cl.other.file, Repeated: true}, nil
					}
					continue // it failed; try again ourselves
				}
				f, err := s.leadComplete(ctx, actor, rt, cl, checksum != nil, supplied)
				if err != nil {
					return CompleteResult{}, err
				}
				return CompleteResult{File: f}, nil
			}
		}
		// A session that finished between our first check and the lookup is
		// not "gone": its completion record answers.
		if errors.Is(err, domain.ErrNotFound) {
			if f, ok := s.completedFile(actor.RoomID, id, key); ok {
				return CompleteResult{File: f, Repeated: true}, nil
			}
		}
		return CompleteResult{}, err
	}
}

// completeClaim is the result of trying to become the complete request of a
// session: either another complete is already running (other), or this
// request now owns call and must interrupt run if it is not nil.
type completeClaim struct {
	call  *completeCall
	run   *appendRun
	other *completeCall
}

// claimComplete makes the caller the one complete request of rt, after the
// checks that need no I/O: the session is alive and has all its bytes.
func (s *Service) claimComplete(rt *sessionRT) (completeClaim, error) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.dead || s.expiredLocked(rt) {
		return completeClaim{}, domain.ErrNotFound
	}
	if rt.completing != nil {
		return completeClaim{other: rt.completing}, nil
	}
	if rt.rec.Offset < rt.rec.Declared {
		return completeClaim{}, &domain.IncompleteError{Offset: rt.rec.Offset, Declared: rt.rec.Declared}
	}
	call := &completeCall{done: make(chan struct{})}
	rt.completing = call
	return completeClaim{call: call, run: rt.active}, nil
}

// leadComplete does the work of the complete request that owns cl.call, and
// settles the call for anyone waiting on it.
func (s *Service) leadComplete(ctx context.Context, actor domain.Device, rt *sessionRT, cl completeClaim, hasSupplied bool, supplied string) (f domain.File, err error) {
	defer func() {
		rt.mu.Lock()
		if rt.completing == cl.call {
			rt.completing = nil
		}
		rt.mu.Unlock()
		cl.call.file, cl.call.err = f, err
		close(cl.call.done)
	}()
	// Past this point the client has been admitted; do not let its
	// disconnection leave the session half-moved.
	bg := context.WithoutCancel(ctx)

	// An append still in flight must record its bytes before they are judged.
	if cl.run != nil {
		cl.run.interrupt()
		if !cl.run.wait(s.interruptDelay()) {
			return domain.File{}, domain.ErrUploadBusy
		}
	}

	rt.mu.Lock()
	rec, dead := rt.rec, rt.dead
	rt.mu.Unlock()
	if dead {
		return domain.File{}, domain.ErrNotFound
	}
	if rec.Offset < rec.Declared { // cannot happen once claimed; keep the guarantee local
		return domain.File{}, &domain.IncompleteError{Offset: rec.Offset, Declared: rec.Declared}
	}

	computed, err := sumFromHashState(rec.HashState)
	if err != nil {
		return domain.File{}, fmt.Errorf("session %s: %w", rec.ID, err)
	}
	if hasSupplied && supplied != computed {
		s.completeTeardown(bg, rt, true).finish(StateFailed)
		return domain.File{}, &domain.ChecksumMismatchError{Supplied: supplied, Computed: computed}
	}

	if err := s.settlePartialSize(bg, rt, rec); err != nil {
		return domain.File{}, err
	}

	integrity := domain.IntegrityUnverified
	if hasSupplied {
		integrity = domain.IntegrityVerified
	}

	file, restored, err := s.publishUnderRoomLock(bg, actor, rt, rec, computed, integrity)
	if err != nil {
		if !restored {
			// The bytes are gone with no metadata; the session cannot recover.
			s.completeTeardown(bg, rt, true).finish(StateFailed)
		}
		return domain.File{}, err
	}

	// Published. Remember the outcome before the session disappears, so a
	// repeat that loses the race still finds it.
	s.rememberCompletion(rt, file)
	s.completeTeardown(bg, rt, false).finish(StateDone)
	return file, nil
}

// publishUnderRoomLock resolves the final name and publishes the finished
// partial of rt inside the room lock, so each completion of the room sees the
// files of the ones before it. restored is as for publishPartial; it is true
// for every failure that happens before the partial is touched.
func (s *Service) publishUnderRoomLock(
	ctx context.Context,
	actor domain.Device,
	rt *sessionRT,
	rec domain.UploadSession,
	computed string,
	integrity domain.Integrity,
) (file domain.File, restored bool, err error) {
	mu := s.roomLock(rt.room)
	mu.Lock()
	defer mu.Unlock()

	rt.mu.Lock()
	dead := rt.dead
	rt.mu.Unlock()
	if dead {
		return domain.File{}, true, domain.ErrNotFound
	}

	existing, err := s.files.ListByRoom(ctx, rt.room)
	if err != nil {
		return domain.File{}, true, fmt.Errorf("list files: %w", err)
	}
	target, err := resolveTarget(existing, actor, rec.Dir, rec.Name, ConflictPolicy(rec.Conflict))
	if err != nil {
		return domain.File{}, true, err
	}
	file = domain.File{
		ID:           domain.FileID(s.ids.NewID()),
		RoomID:       rt.room,
		Dir:          rec.Dir,
		Name:         target.Name,
		Size:         rec.Declared,
		UploaderID:   actor.ID,
		UploaderName: actor.Name,
		CreatedAt:    s.clock.Now(),
		SHA256:       computed,
		Integrity:    integrity,
	}
	restored, err = s.publishPartial(ctx, rt.id, file, target.Replace)
	if err != nil {
		return domain.File{}, restored, err
	}
	return file, true, nil
}

// settlePartialSize makes sure the partial holds exactly the Declared bytes
// before it is promoted. Extra bytes (a tail an interrupted append left) are
// cut off; a partial that is missing or shorter than the confirmed offset
// cannot be repaired, so the session is discarded and reported as gone.
func (s *Service) settlePartialSize(ctx context.Context, rt *sessionRT, rec domain.UploadSession) error {
	size, err := s.upDeps.Partials.Size(ctx, rt.room, rt.id)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return fmt.Errorf("measure partial file: %w", err)
	}
	if err != nil || size < rec.Declared {
		slog.Warn("partial file of an upload is shorter than its confirmed offset; discarding the session", "session", rt.id)
		s.completeTeardown(ctx, rt, true).finish(StateFailed)
		return fmt.Errorf("partial file is shorter than the confirmed offset: %w", domain.ErrNotFound)
	}
	if size > rec.Declared {
		if err := s.upDeps.Partials.Truncate(ctx, rt.room, rt.id, rec.Declared); err != nil {
			return fmt.Errorf("cut partial file to its declared size: %w", err)
		}
	}
	return nil
}

// completeTeardown ends a session that completed, or that was rejected or
// lost while completing: it is marked dead, leaves the registry, gives back
// its disk reservation and activity hold, and its persisted record (and the
// partial file, when deletePartial) is deleted. It returns the session's
// progress tracker for the caller to close with the final state.
//
// Cleanup failures are logged, not returned: a stray record is removed by
// Recover at the next start because its partial is gone or unused.
func (s *Service) completeTeardown(ctx context.Context, rt *sessionRT, deletePartial bool) *tracker {
	rt.mu.Lock()
	rt.dead = true
	res := rt.res
	tr := s.trackerLocked(rt)
	rt.mu.Unlock()

	s.unregisterSession(rt)
	if res != nil {
		res.Release()
	}
	s.activity().Release(string(rt.id))
	if deletePartial {
		if err := s.upDeps.Partials.Delete(ctx, rt.room, rt.id); err != nil {
			slog.Warn("cannot delete the partial file of a finished upload", "session", rt.id, "error", err)
		}
	}
	if err := s.upDeps.Sessions.Delete(ctx, rt.room, rt.id); err != nil {
		slog.Warn("cannot delete the record of a finished upload", "session", rt.id, "error", err)
	}
	return tr
}
