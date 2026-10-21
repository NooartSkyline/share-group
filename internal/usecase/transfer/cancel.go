package transfer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"lanshare/internal/domain"
)

// SessionSummary is what the Owner of a room sees of one unfinished upload
// session. It deliberately has no Resume_Key and no file name: the Owner can
// manage the session (CancelByOwner) without either, and neither belongs in
// a list that may end up on a screen or in a log.
type SessionSummary struct {
	ID domain.SessionID
	// Declared is the size the sender announced; Offset is the Confirmed_Offset.
	Declared int64
	Offset   int64
	// LastActivity is the time of the last append that wrote a byte, or the
	// creation time of a session that never received one.
	LastActivity time.Time
}

// CancelSession ends an upload session on behalf of the holder of its
// Resume_Key, or of the Owner of the session's room, who needs no key. An
// Owner never reaches into another room: the session is looked up in the
// actor's own room first.
//
// Order of the checks: role (ErrForbidden), then the session. Every reason
// the actor may not see the session (unknown id, another room, wrong or empty
// key for a non-owner, dead or expired session) answers the same unwrapped
// domain.ErrNotFound and changes nothing. A session whose complete is running
// answers domain.ErrUploadBusy and is left alone.
//
// The session is marked dead first, so an append in flight stops saving. That
// append is interrupted and given interruptDelay to record its bytes and
// stop; then the registry slot, the persisted record, the partial file, the
// disk reservation and the activity hold are released, and the room is told
// "stopped" exactly once. Of several concurrent cancels one wins and the rest
// get ErrNotFound. Once the session is marked dead cleanup always runs to the
// end; a failure to delete is reported afterwards. The resume key is never
// logged.
func (s *Service) CancelSession(ctx context.Context, actor domain.Device, id domain.SessionID, key string) error {
	if !actor.Role.CanUpload() {
		return domain.ErrForbidden
	}
	if !s.uploadsConfigured() {
		return domain.ErrNotFound
	}
	var (
		rt  *sessionRT
		err error
	)
	if actor.Role.CanManage() {
		rt, err = s.lookupInRoom(actor.RoomID, id)
	} else {
		rt, err = s.lookupSession(actor.RoomID, id, key)
	}
	if err != nil {
		return err
	}
	return s.cancel(ctx, rt)
}

// CancelByOwner cancels any unfinished session of the owner's room without
// its Resume_Key (Req 14.21). Only the Owner may; a session that does not
// exist, belongs to another room or is already finished is ErrNotFound.
func (s *Service) CancelByOwner(ctx context.Context, owner domain.Device, id domain.SessionID) error {
	if !owner.Role.CanManage() {
		return domain.ErrForbidden
	}
	if !s.uploadsConfigured() {
		return domain.ErrNotFound
	}
	rt, err := s.lookupInRoom(owner.RoomID, id)
	if err != nil {
		return err
	}
	return s.cancel(ctx, rt)
}

// ListSessions returns the unfinished sessions of the owner's room, and of no
// other room, newest activity first. Dead and expired sessions are left out.
// The result carries no Resume_Key and no file name.
func (s *Service) ListSessions(_ context.Context, owner domain.Device) ([]SessionSummary, error) {
	if !owner.Role.CanManage() {
		return nil, domain.ErrForbidden
	}
	if !s.uploadsConfigured() {
		return nil, nil
	}
	var out []SessionSummary
	for _, rt := range s.snapshotSessions() {
		if rt.room != owner.RoomID {
			continue
		}
		rt.mu.Lock()
		live := !rt.dead && !s.expiredLocked(rt)
		rec := rt.rec
		rt.mu.Unlock()
		if !live {
			continue
		}
		last := rec.CreatedAt
		if rec.LastWriteAt.After(last) {
			last = rec.LastWriteAt
		}
		out = append(out, SessionSummary{ID: rec.ID, Declared: rec.Declared, Offset: rec.Offset, LastActivity: last})
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].LastActivity.Equal(out[j].LastActivity) {
			return out[i].LastActivity.After(out[j].LastActivity)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// PartialBytes is the total of the Confirmed_Offset of every live unfinished
// session, in all rooms: the bytes of partial files the server is holding.
func (s *Service) PartialBytes() int64 {
	var total int64
	for _, rt := range s.snapshotSessions() {
		rt.mu.Lock()
		if !rt.dead {
			total += rt.rec.Offset
		}
		rt.mu.Unlock()
	}
	return total
}

// snapshotSessions copies the registry, so callers lock each session without
// holding regMu (lock order regMu -> sessionRT.mu is kept either way).
func (s *Service) snapshotSessions() []*sessionRT {
	s.regMu.Lock()
	defer s.regMu.Unlock()
	list := make([]*sessionRT, 0, len(s.sessions))
	for _, rt := range s.sessions {
		list = append(list, rt)
	}
	return list
}

// lookupInRoom finds the live session id in room without asking for its key.
// It is lookupSession for callers that are allowed to skip the key (the
// Owner), and gives the same unwrapped domain.ErrNotFound for every failure.
func (s *Service) lookupInRoom(room domain.RoomID, id domain.SessionID) (*sessionRT, error) {
	s.regMu.Lock()
	rt := s.sessions[id]
	s.regMu.Unlock()
	if rt == nil || rt.room != room {
		return nil, domain.ErrNotFound
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.dead || s.expiredLocked(rt) {
		return nil, domain.ErrNotFound
	}
	return rt, nil
}

// cancel ends rt for good. The caller has already proven the right to do so.
func (s *Service) cancel(ctx context.Context, rt *sessionRT) error {
	rt.mu.Lock()
	if rt.dead || s.expiredLocked(rt) {
		rt.mu.Unlock()
		return domain.ErrNotFound
	}
	if rt.completing != nil {
		// The partial file is being turned into a stored file; the client may
		// try again once that has succeeded or failed.
		rt.mu.Unlock()
		return domain.ErrUploadBusy
	}
	rt.dead = true
	run := rt.active
	res := rt.res
	rt.res = nil
	tr := s.trackerLocked(rt)
	confirmed := rt.rec.Offset
	rt.mu.Unlock()

	// Whoever set dead owns the cleanup from here. An append in flight no
	// longer saves; make it stop and let it truncate its tail before the
	// file goes. If it does not stop in time, cancel anyway: the append can
	// no longer record anything.
	if run != nil {
		run.interrupt()
		run.wait(s.interruptDelay())
	}

	bg := context.WithoutCancel(ctx)
	s.unregisterSession(rt)
	var errs []error
	if err := s.upDeps.Sessions.Delete(bg, rt.room, rt.id); err != nil {
		errs = append(errs, fmt.Errorf("delete upload session: %w", err))
	}
	if err := s.upDeps.Partials.Delete(bg, rt.room, rt.id); err != nil {
		errs = append(errs, fmt.Errorf("delete partial file: %w", err))
	}
	if res != nil {
		res.Release()
	}
	s.activity().Release(string(rt.id))

	// The room hears "stopped" once, with the bytes the server really holds;
	// finish ignores a tracker that is already closed.
	tr.rewind(confirmed)
	tr.finish(StateStopped)

	if err := errors.Join(errs...); err != nil {
		slog.Warn("cancelled upload left files behind", "error", err)
		return fmt.Errorf("cancel upload: %w", err)
	}
	return nil
}
