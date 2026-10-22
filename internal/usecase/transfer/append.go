package transfer

import (
	"context"
	"errors"
	"fmt"
	"hash"
	"io"
	"log/slog"
	"sync"
	"time"

	"lanshare/internal/domain"
	"lanshare/internal/usecase/port"
)

const (
	// chunkSize is the most file content one append request holds in memory,
	// and the size of the buffers other streaming code (checksums) can borrow
	// from chunkPool.
	chunkSize = 1 << 20

	// persistEvery is how many bytes an append may write before it makes them
	// durable and confirmed in the middle of the request, which bounds the
	// work lost when the process is killed during a long request.
	persistEvery = 64 << 20

	// holdAfterAppend keeps the Mac awake after an append that left its
	// session unfinished, for the client to resume.
	holdAfterAppend = 60 * time.Second

	// maxEmptyReads is how many reads in a row may return neither a byte nor
	// an error before the body is treated as broken (as bufio does).
	maxEmptyReads = 100
)

// chunkPool holds the chunkSize buffers of appends and other streaming code.
// Buffers are borrowed whole and carry stale bytes: callers write before they
// read.
var chunkPool = sync.Pool{New: func() any {
	b := make([]byte, chunkSize)
	return &b
}}

func getChunk() *[]byte  { return chunkPool.Get().(*[]byte) }
func putChunk(b *[]byte) { chunkPool.Put(b) }

// errSessionGone is returned by saveSession for a session that was cancelled,
// expired or finished meanwhile; whoever ended it owns the cleanup.
var errSessionGone = errors.New("upload session is gone")

// AppendResult is what an append reports, also alongside an error.
type AppendResult struct {
	// Offset is the Confirmed_Offset after the request: bytes durably written
	// and recorded. It is accurate when Append returns an error too (after an
	// interruption, a stall, a rollback), so the caller can tell the client
	// where to resume. Only a request rejected before it was admitted (wrong
	// role, unknown session, offset mismatch, busy) leaves it zero.
	Offset int64
}

// Append writes the body of one request to the partial file of the session,
// continuing at offset, which must equal the Confirmed_Offset. contentLength
// is the declared length of the body, or negative when unknown.
//
// Order of the checks, each failure leaving the session untouched: role
// (ErrForbidden), session and resume key (ErrNotFound, one answer for every
// reason), offset range (ErrInvalidInput), a competing append (the older one
// is interrupted and given interruptWait to record its bytes, else
// ErrUploadBusy), offset equal to the Confirmed_Offset
// (*domain.OffsetMismatchError), contentLength within the Declared size
// (*domain.SizeExceededError, before any body byte is read).
//
// Then the body is copied in chunks of at most 1 MiB, writing to the partial
// first and hashing exactly the bytes the writer accepted. Every persistEvery
// bytes and when the request ends, for whatever reason, the partial is synced
// and only then the record (Offset and SHA-256 state together) is saved, and
// only after that does it become the session's Confirmed_Offset, so the record
// never claims more than the disk holds. Persisting ignores cancellation of
// ctx: a client that left still has its bytes recorded.
//
// How the request ends:
//   - the body ends with io.EOF: nil, even below the Declared size;
//   - the body fails, ctx is cancelled or Interrupt is called: the bytes
//     received so far are recorded and the error is domain.ErrInterrupted, or
//     domain.ErrStallTimeout when no byte arrived for the stall timeout;
//   - the body holds more than the Declared size: the whole request is rolled
//     back and the error is *domain.SizeExceededError;
//   - the disk is full: the bytes accepted are recorded and the error is
//     *domain.InsufficientStorageError;
//   - the record cannot be saved: the partial is truncated to the last saved
//     offset and the error is *domain.PersistFailedError.
//
// Whenever the request ends below the Declared size the room is told the
// transfer is interrupted. The tracker, the activity hold and the ledger
// follow the persisted offset. The resume key is never logged.
func (s *Service) Append(
	ctx context.Context,
	actor domain.Device,
	id domain.SessionID,
	key string,
	offset int64,
	contentLength int64,
	body AppendBody,
) (AppendResult, error) {
	if !actor.CanUpload() {
		return AppendResult{}, domain.ErrForbidden
	}
	if !s.uploadsConfigured() {
		return AppendResult{}, domain.ErrNotFound
	}
	rt, err := s.lookupSession(actor.RoomID, id, key)
	if err != nil {
		return AppendResult{}, err
	}
	if body == nil {
		return AppendResult{}, fmt.Errorf("append body is required: %w", domain.ErrInvalidInput)
	}

	run := newAppendRun(body)
	base, tr, err := s.admitAppend(rt, offset, contentLength, run)
	if err != nil {
		return AppendResult{}, err
	}

	// From here rt.active is ours; release clears it on every way out.
	act := s.activity()
	end := act.Begin()
	act.Release(string(id))
	op := &appendOp{
		s: s, bg: context.WithoutCancel(ctx), rt: rt, run: run, tr: tr,
		base: base, saved: base, live: base.Offset,
	}
	defer op.release(end)
	return op.execute(ctx, body)
}

// admitAppend makes run the one active append of rt, after the checks that
// need no I/O. It returns the persisted record the request starts from and
// the session's tracker.
//
// An append already running is interrupted and waited for (at most
// interruptDelay in total, however many appends take turns), because the
// offset it records decides whether this request's offset is right.
func (s *Service) admitAppend(rt *sessionRT, offset, contentLength int64, run *appendRun) (domain.UploadSession, *tracker, error) {
	var timeout *time.Timer
	defer func() {
		if timeout != nil {
			timeout.Stop()
		}
	}()
	for {
		rt.mu.Lock()
		if rt.dead || s.expiredLocked(rt) {
			rt.mu.Unlock()
			return domain.UploadSession{}, nil, domain.ErrNotFound
		}
		rec := rt.rec
		if offset < 0 || offset > rec.Declared {
			rt.mu.Unlock()
			return domain.UploadSession{}, nil, fmt.Errorf("offset must be 0 to %d: %w", rec.Declared, domain.ErrInvalidInput)
		}
		if rt.completing != nil {
			// A complete is turning the partial into a file; nothing may be
			// appended meanwhile, and if it fails the client can retry.
			rt.mu.Unlock()
			return domain.UploadSession{}, nil, domain.ErrUploadBusy
		}
		old := rt.active
		if old == nil {
			if offset != rec.Offset {
				rt.mu.Unlock()
				return domain.UploadSession{}, nil, &domain.OffsetMismatchError{Offset: rec.Offset}
			}
			// Compared this way round so a huge contentLength cannot overflow.
			if contentLength >= 0 && contentLength > rec.Declared-offset {
				rt.mu.Unlock()
				return domain.UploadSession{}, nil, &domain.SizeExceededError{}
			}
			rt.active = run
			tr := s.trackerLocked(rt)
			rt.mu.Unlock()
			return rec, tr, nil
		}
		rt.mu.Unlock()

		old.interrupt()
		if timeout == nil {
			timeout = time.NewTimer(s.interruptDelay())
		}
		select {
		case <-old.done:
		case <-timeout.C:
			return domain.UploadSession{}, nil, domain.ErrUploadBusy
		}
	}
}

// appendOp is one admitted append request. Its fields are used by the
// request's goroutine only; what other goroutines may see lives in sessionRT.
type appendOp struct {
	s   *Service
	bg  context.Context // ctx without cancellation, for everything that persists
	rt  *sessionRT
	run *appendRun
	tr  *tracker

	// base is the persisted record when the request was admitted: where a
	// rolled-back request returns to. saved is the newest record this request
	// persisted, equal to base until the first persist.
	base, saved domain.UploadSession

	w    port.PartialWriter
	h    hash.Hash
	live int64 // bytes in the partial and in h; saved.Offset <= live
}

// bodyEnd records why the copy loop stopped. At most one field is set; none
// means the body ended cleanly.
type bodyEnd struct {
	readErr  error // body failed, or ctx was cancelled
	writeErr error // the partial could not take the bytes (disk full excluded)
	diskFull bool
	exceeded bool  // the body holds more than the Declared size
	persist  error // a mid-request persist failed
}

// execute copies the body into the partial and settles the outcome.
func (op *appendOp) execute(ctx context.Context, body AppendBody) (AppendResult, error) {
	s := op.s
	h, err := restoreHashState(op.base.HashState)
	if err != nil {
		return op.result(), fmt.Errorf("resume upload: %w", err)
	}
	op.h = h
	w, err := s.upDeps.Partials.OpenAppend(ctx, op.rt.room, op.rt.id, op.base.Offset)
	if err != nil {
		if ctx.Err() != nil {
			return op.result(), domain.ErrInterrupted
		}
		return op.result(), fmt.Errorf("open partial file: %w", err)
	}
	defer w.Close() // runs before release, so a replacing append never meets an open writer
	op.w = w

	bufp := getChunk()
	defer putChunk(bufp)
	buf := *bufp

	// The watchdog runs only while a Read is pending, so slow disk work on our
	// side is never mistaken for a silent client.
	stall := s.upCfg.StallTimeout
	wd := time.AfterFunc(stall, op.run.stall)
	defer wd.Stop()

	var (
		end   bodyEnd
		empty int
	)
	for {
		if err := ctx.Err(); err != nil {
			end.readErr = err
			break
		}
		wd.Reset(stall)
		n, rerr := body.Read(buf)
		wd.Stop()

		if n > 0 {
			empty = 0
			if int64(n) > op.base.Declared-op.live {
				end.exceeded = true
				break
			}
			// File first, then hash exactly what the file accepted, then count.
			wn, werr := w.Write(buf[:n])
			if wn < 0 || wn > n {
				wn = 0
				werr = io.ErrShortWrite
			}
			if wn > 0 {
				_, _ = op.h.Write(buf[:wn])
				op.live += int64(wn)
				op.tr.add(wn)
			}
			if werr == nil && wn < n {
				werr = io.ErrShortWrite
			}
			if werr != nil {
				if errors.Is(werr, domain.ErrDiskFull) {
					end.diskFull = true
				} else {
					end.writeErr = werr
				}
				break
			}
			if op.live-op.saved.Offset >= persistEvery {
				if err := op.persist(); err != nil {
					end.persist = err
					break
				}
			}
		} else if rerr == nil {
			if empty++; empty >= maxEmptyReads {
				end.readErr = io.ErrNoProgress
				break
			}
		}
		if rerr != nil {
			if !errors.Is(rerr, io.EOF) {
				end.readErr = rerr
			}
			break
		}
	}
	return op.settle(end)
}

// settle turns the way the copy loop ended into the persisted state, the
// tracker events and the returned error.
func (op *appendOp) settle(end bodyEnd) (AppendResult, error) {
	if end.exceeded {
		return op.rollbackExceeded()
	}
	if end.persist != nil {
		return op.failPersist(end.persist)
	}
	if end.diskFull || end.writeErr != nil {
		// The writer may hold more than it reported accepting. Make the file
		// exactly as long as the hash and the offset say.
		op.truncate(op.live)
	}
	if err := op.persist(); err != nil {
		return op.failPersist(err)
	}
	op.conclude()

	switch {
	case end.diskFull:
		return op.result(), op.insufficient()
	case end.writeErr != nil:
		return op.result(), fmt.Errorf("write partial file: %w", end.writeErr)
	case end.readErr != nil:
		if op.run.stalled.Load() {
			return op.result(), domain.ErrStallTimeout
		}
		return op.result(), domain.ErrInterrupted
	}
	return op.result(), nil
}

func (op *appendOp) result() AppendResult { return AppendResult{Offset: op.saved.Offset} }

// persist makes every byte written so far durable and confirmed, in the one
// order that keeps the record from running ahead of the disk: Sync the
// partial, save the record with the matching SHA-256 state, and only then
// let it replace the session's Confirmed_Offset. It does nothing when no byte
// was written since the last persist, so an empty request never touches the
// record (and never renews the session's expiry, Req 4.3).
func (op *appendOp) persist() error {
	if op.live == op.saved.Offset {
		return nil
	}
	if err := op.w.Sync(); err != nil {
		return fmt.Errorf("sync partial file: %w", err)
	}
	state, err := marshalHashState(op.h)
	if err != nil {
		return err
	}
	rec := op.saved
	rec.Offset = op.live
	rec.HashState = state
	rec.LastWriteAt = op.s.clock.Now()
	if err := op.s.saveSession(op.bg, op.rt, rec); err != nil {
		return err
	}
	op.saved = rec
	return nil
}

// saveSession durably saves rec and only then makes it the session's
// Confirmed_Offset and updates the disk reservation to the bytes still to come.
// A failed Save leaves rt.rec as it was and returns a *domain.PersistFailedError
// naming that offset; a session that is dead meanwhile returns errSessionGone
// without saving, because its record is about to be deleted. Callers need not
// hold rt.mu.
func (s *Service) saveSession(ctx context.Context, rt *sessionRT, rec domain.UploadSession) error {
	rt.mu.Lock()
	dead, confirmed := rt.dead, rt.rec.Offset
	rt.mu.Unlock()
	if dead {
		return errSessionGone
	}
	if err := s.upDeps.Sessions.Save(ctx, rec); err != nil {
		return &domain.PersistFailedError{Offset: confirmed, Err: fmt.Errorf("save upload session: %w", err)}
	}
	rt.mu.Lock()
	rt.rec = rec
	res := rt.res
	rt.mu.Unlock()
	if res != nil {
		res.Update(uint64(rec.Declared - rec.Offset))
	}
	return nil
}

// failPersist ends a request whose bytes could not be made durable. The
// partial goes back to the last saved offset, the in-memory state was never
// moved, and the error names that offset. A session that was cancelled
// meanwhile is left to whoever cancelled it. A full disk while syncing is
// reported as such.
func (op *appendOp) failPersist(cause error) (AppendResult, error) {
	if errors.Is(cause, errSessionGone) {
		return op.result(), domain.ErrInterrupted
	}
	if op.live > op.saved.Offset {
		op.truncate(op.saved.Offset)
	}
	op.live = op.saved.Offset
	op.conclude()

	var pf *domain.PersistFailedError
	switch {
	case errors.As(cause, &pf):
		pf.Offset = op.saved.Offset
		return op.result(), pf
	case errors.Is(cause, domain.ErrDiskFull):
		return op.result(), op.insufficient()
	}
	return op.result(), &domain.PersistFailedError{Offset: op.saved.Offset, Err: cause}
}

// rollbackExceeded undoes the whole request: the body holds more than the
// Declared size, so none of its bytes may stay (Req 2.5). If a mid-request
// persist already confirmed some of them, the original record is saved again
// first and only then is the partial cut back, so the record never runs ahead
// of the file.
func (op *appendOp) rollbackExceeded() (AppendResult, error) {
	if op.saved.Offset != op.base.Offset {
		if err := op.s.saveSession(op.bg, op.rt, op.base); err != nil {
			return op.failPersist(err) // the record keeps what was confirmed; so does the file
		}
		op.saved = op.base
	}
	if op.live > op.base.Offset {
		op.truncate(op.base.Offset)
	}
	op.live = op.base.Offset
	op.conclude()
	return op.result(), &domain.SizeExceededError{}
}

// conclude tells the room where the transfer stands now that the persisted
// offset is final: the tracker returns to it if bytes it counted were rolled
// back, and a session still short of its Declared size is interrupted.
func (op *appendOp) conclude() {
	op.tr.rewind(op.saved.Offset)
	if op.saved.Offset < op.saved.Declared {
		op.tr.mark(StateInterrupted)
	}
}

// insufficient builds the error for a full disk: the bytes the session still
// has to write plus the reserve, against the free space read now (Req 7.5).
// An unreadable free space counts as none available.
func (op *appendOp) insufficient() error {
	snap := op.s.upDeps.Guard.Snapshot(op.bg)
	remaining := uint64(op.saved.Declared - op.saved.Offset)
	required := remaining + snap.MinFree
	if required < remaining { // overflow: saturate
		required = ^uint64(0)
	}
	var avail uint64
	if snap.FreeKnown {
		avail = snap.Free
	}
	return &domain.InsufficientStorageError{Required: required, Available: avail}
}

// truncate shortens the partial to size. A failure is only logged: the next
// append truncates to the persisted offset when it opens the file, and so does
// Recover, so a leftover tail is harmless.
func (op *appendOp) truncate(size int64) {
	if err := op.s.upDeps.Partials.Truncate(op.bg, op.rt.room, op.rt.id, size); err != nil {
		slog.Warn("cannot cut back the partial file of an upload", "error", err)
	}
}

// release ends the request on every path: the session may take a new append,
// an unfinished session keeps the Mac awake for a while, and finally waiters
// learn that the persisted offset is final.
func (op *appendOp) release(endActivity func()) {
	rt := op.rt
	rt.mu.Lock()
	if rt.active == op.run {
		rt.active = nil
	}
	unfinished := !rt.dead && rt.rec.Offset < rt.rec.Declared
	rt.mu.Unlock()
	if unfinished {
		op.s.activity().Hold(string(rt.id), holdAfterAppend)
	}
	endActivity()
	op.run.finish()
}
