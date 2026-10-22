package transfer

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"lanshare/internal/domain"
	"lanshare/internal/usecase/port"
	"lanshare/internal/usecase/storage"
)

const (
	// MaxSessionsPerDevice and MaxSessionsPerRoom bound unfinished upload
	// sessions, so a client cannot fill the disk ledger with empty sessions.
	MaxSessionsPerDevice = 16
	MaxSessionsPerRoom   = 64

	// DefaultUploadTTL, DefaultStallTimeout and DefaultCompletionKeep apply
	// when ConfigureUploads is given a zero value for the setting.
	DefaultUploadTTL      = 24 * time.Hour
	DefaultStallTimeout   = 30 * time.Second
	DefaultCompletionKeep = 10 * time.Minute

	// interruptWait is how long a new append, a cancel or a complete waits for
	// the append it cut off to record its bytes before giving up.
	interruptWait = 5 * time.Second
)

// UploadDeps are the collaborators of resumable uploads.
type UploadDeps struct {
	Sessions port.UploadSessionRepository
	Partials port.PartialStore
	Guard    *storage.Guard
	Secrets  port.SecretGenerator
	// Activity may be nil, which means "do nothing" (no power management).
	Activity port.ActivityTracker
}

// UploadConfig holds the tunable timings of resumable uploads. A zero field
// takes its Default... value.
type UploadConfig struct {
	// TTL is how long an idle session survives (-upload-ttl).
	TTL time.Duration
	// StallTimeout ends an append that receives no byte for this long.
	StallTimeout time.Duration
	// CompletionKeep is how long a finished session still answers a repeated
	// complete request with the same file.
	CompletionKeep time.Duration

	// interruptWait replaces the package default for how long Append, Complete
	// and Cancel wait for an interrupted append. Zero keeps the default; only
	// tests in this package set it, so they need not wait five real seconds.
	interruptWait time.Duration

	// janitorEvery and shutdownWait replace janitorInterval and shutdownGrace
	// in tests of this package. Zero keeps the default.
	janitorEvery time.Duration
	shutdownWait time.Duration
}

// interruptDelay is how long to wait for an append that was cut off to record
// its bytes.
func (s *Service) interruptDelay() time.Duration {
	if s.upCfg.interruptWait > 0 {
		return s.upCfg.interruptWait
	}
	return interruptWait
}

// ConfigureUploads switches on resumable upload sessions. Call it once at
// start-up, before the service is used from several goroutines. Until it is
// called every session operation reports domain.ErrNotFound and the rest of
// the service works as before.
func (s *Service) ConfigureUploads(d UploadDeps, c UploadConfig) {
	if c.TTL <= 0 {
		c.TTL = DefaultUploadTTL
	}
	if c.StallTimeout <= 0 {
		c.StallTimeout = DefaultStallTimeout
	}
	if c.CompletionKeep <= 0 {
		c.CompletionKeep = DefaultCompletionKeep
	}
	s.upDeps, s.upCfg = d, c
}

// uploadsConfigured reports whether ConfigureUploads has run.
func (s *Service) uploadsConfigured() bool { return s.upDeps.Sessions != nil }

// activity returns the configured ActivityTracker, or one that does nothing.
func (s *Service) activity() port.ActivityTracker {
	if s.upDeps.Activity == nil {
		return noActivity{}
	}
	return s.upDeps.Activity
}

type noActivity struct{}

func (noActivity) Begin() func()              { return func() {} }
func (noActivity) Hold(string, time.Duration) {}
func (noActivity) Release(string)             {}

// AppendBody is the request body of an append. Interrupt makes a Read that is
// blocked, and every later Read, return an error at once. Cancelling a
// context does not wake a blocked network read, so the HTTP adapter supplies
// this (it moves the connection's read deadline).
type AppendBody interface {
	io.Reader
	Interrupt()
}

// appendRun is the one append request currently writing to a session. It is
// created and cleared under sessionRT.mu; its own state is safe without it.
type appendRun struct {
	body AppendBody
	// done is closed once the run has recorded its bytes (or given up), so a
	// waiter knows the session offset is final.
	done chan struct{}

	doneOnce      sync.Once
	interruptOnce sync.Once
	stalled       atomic.Bool
}

func newAppendRun(body AppendBody) *appendRun {
	return &appendRun{body: body, done: make(chan struct{})}
}

// interrupt cuts the request short. It is idempotent, so the stall watchdog,
// a replacing append, a cancel and shutdown can all call it.
func (r *appendRun) interrupt() {
	r.interruptOnce.Do(r.body.Interrupt)
}

// stall is interrupt for a Stall_Timeout, remembered so the request ends as
// stall_timeout rather than as a plain disconnect.
func (r *appendRun) stall() {
	r.stalled.Store(true)
	r.interrupt()
}

// finish marks the run recorded. Safe to call more than once.
func (r *appendRun) finish() { r.doneOnce.Do(func() { close(r.done) }) }

// wait reports whether the run finished within d.
func (r *appendRun) wait(d time.Duration) bool {
	select {
	case <-r.done:
		return true
	default:
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-r.done:
		return true
	case <-t.C:
		return false
	}
}

// completeCall is one complete request in flight. Concurrent completes of the
// same session wait on done and share the outcome.
type completeCall struct {
	done chan struct{}
	file domain.File
	err  error
}

// sessionRT is the in-memory side of an upload session.
//
// id, room, creator and keyHash never change after creation and are read
// without a lock. Everything below mu is guarded by it. mu is never held
// while reading a request body or writing a large chunk to disk.
type sessionRT struct {
	id      domain.SessionID
	room    domain.RoomID
	creator domain.DeviceID
	keyHash string // hex SHA-256 of the resume key

	mu sync.Mutex
	// rec is the last persisted record; rec.Offset is the Confirmed_Offset.
	rec domain.UploadSession
	// active is the append request currently writing, if any.
	active *appendRun
	// completing is the complete request currently running, if any.
	completing *completeCall
	// dead is set when the session is cancelled, expired or finished; a dead
	// session answers every lookup as not found.
	dead bool
	// res is the disk space this session still has reserved.
	res *storage.Reservation
	// tr reports progress for the whole life of the session; created by the
	// first append.
	tr *tracker
}

func newSessionRT(rec domain.UploadSession, res *storage.Reservation) *sessionRT {
	return &sessionRT{
		id:      rec.ID,
		room:    rec.RoomID,
		creator: rec.CreatorID,
		keyHash: rec.ResumeKeyHash,
		rec:     rec,
		res:     res,
	}
}

// trackerLocked returns the session's progress tracker, creating it on first
// use. Its bytes start at the Confirmed_Offset, so a session recovered after a
// restart does not report progress from zero. The caller holds rt.mu.
func (s *Service) trackerLocked(rt *sessionRT) *tracker {
	if rt.tr == nil {
		t := s.newTracker(
			domain.Device{ID: rt.rec.CreatorID, Name: rt.rec.CreatorName, RoomID: rt.rec.RoomID},
			string(rt.rec.ID), joinPath(rt.rec.Dir, rt.rec.Name), DirectionUpload, rt.rec.Declared,
		)
		t.p.Bytes, t.lastN = rt.rec.Offset, rt.rec.Offset
		rt.tr = t
	}
	return rt.tr
}

// zeroKeyHash stands in for the stored hash of a session that does not exist,
// so a lookup does the same work whether or not the session is there.
const zeroKeyHash = "0000000000000000000000000000000000000000000000000000000000000000"

// keyMatches reports whether key hashes to stored. It always hashes the key
// and compares in constant time, and never matches an empty key or the
// placeholder hash.
func keyMatches(stored, key string) bool {
	got := domain.HashResumeKey(key)
	eq := subtle.ConstantTimeCompare([]byte(got), []byte(stored)) == 1
	return eq && key != "" && stored != zeroKeyHash
}

// lookupSession finds the live session id in room, proving possession of key.
//
// Every failure (no such session, a session of another room, an empty or
// wrong key, a dead or expired session, uploads not configured) returns the
// same domain.ErrNotFound, unwrapped, so a caller cannot tell them apart. The
// key is compared against the zero hash when no session matches, so the work
// done does not reveal whether the id exists.
//
// Callers must check the role (CanUpload) first. The returned session may
// still die before the caller locks it; state changes must re-check rt.dead
// under rt.mu.
func (s *Service) lookupSession(room domain.RoomID, id domain.SessionID, key string) (*sessionRT, error) {
	s.regMu.Lock()
	rt := s.sessions[id]
	s.regMu.Unlock()
	if rt != nil && rt.room != room {
		rt = nil // another room's session is indistinguishable from none
	}
	stored := zeroKeyHash
	if rt != nil {
		stored = rt.keyHash
	}
	ok := keyMatches(stored, key)
	if rt == nil || !ok {
		return nil, domain.ErrNotFound
	}

	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.dead || s.expiredLocked(rt) {
		return nil, domain.ErrNotFound
	}
	return rt, nil
}

// expiredLocked reports whether the session has outlived its TTL. A session
// that is receiving bytes is never expired, however long the upload takes.
// The caller holds rt.mu.
func (s *Service) expiredLocked(rt *sessionRT) bool {
	if rt.active != nil {
		return false
	}
	return !s.clock.Now().Before(rt.rec.ExpiresAt(s.upCfg.TTL))
}

// registerSession adds rt to the registry unless its creator or its room has
// reached the limit of unfinished sessions.
func (s *Service) registerSession(rt *sessionRT) error {
	s.regMu.Lock()
	defer s.regMu.Unlock()
	if s.perDevice[rt.creator] >= MaxSessionsPerDevice || s.perRoom[rt.room] >= MaxSessionsPerRoom {
		return domain.ErrTooManyUploads
	}
	if _, dup := s.sessions[rt.id]; dup {
		return domain.ErrInvalidInput
	}
	s.sessions[rt.id] = rt
	s.perDevice[rt.creator]++
	s.perRoom[rt.room]++
	return nil
}

// unregisterSession removes rt from the registry. Calling it again, or for a
// session that was never registered, does nothing.
func (s *Service) unregisterSession(rt *sessionRT) {
	s.regMu.Lock()
	defer s.regMu.Unlock()
	if s.sessions[rt.id] != rt {
		return
	}
	delete(s.sessions, rt.id)
	if s.perDevice[rt.creator]--; s.perDevice[rt.creator] <= 0 {
		delete(s.perDevice, rt.creator)
	}
	if s.perRoom[rt.room]--; s.perRoom[rt.room] <= 0 {
		delete(s.perRoom, rt.room)
	}
}

// roomLock returns the mutex that serialises publishing files into room:
// choosing a name, saving metadata and removing an overwritten file. It is
// taken after any sessionRT.mu and before the leaf locks. Never call it with
// regMu held.
func (s *Service) roomLock(room domain.RoomID) *sync.Mutex {
	s.regMu.Lock()
	defer s.regMu.Unlock()
	m := s.roomLocks[room]
	if m == nil {
		m = new(sync.Mutex)
		s.roomLocks[room] = m
	}
	return m
}

// completionRecord remembers a finished session for a while, so a client that
// lost the response to complete can repeat it and get the same file back.
type completionRecord struct {
	room    domain.RoomID
	keyHash string
	file    domain.File
	at      time.Time
}

// rememberCompletion stores the outcome of a successful complete.
func (s *Service) rememberCompletion(rt *sessionRT, f domain.File) {
	s.regMu.Lock()
	defer s.regMu.Unlock()
	s.completions[rt.id] = completionRecord{room: rt.room, keyHash: rt.keyHash, file: f, at: s.clock.Now()}
}

// completedFile returns the file of a session that completed less than
// CompletionKeep ago, if key proves possession of it. As with lookupSession,
// the key is compared against the zero hash when there is no such record.
func (s *Service) completedFile(room domain.RoomID, id domain.SessionID, key string) (domain.File, bool) {
	s.regMu.Lock()
	rec, found := s.completions[id]
	s.regMu.Unlock()
	if found && (rec.room != room || !s.clock.Now().Before(rec.at.Add(s.upCfg.CompletionKeep))) {
		found = false
	}
	stored := zeroKeyHash
	if found {
		stored = rec.keyHash
	}
	if !keyMatches(stored, key) || !found {
		return domain.File{}, false
	}
	return rec.file, true
}

// dropExpiredCompletions forgets completion records older than CompletionKeep.
func (s *Service) dropExpiredCompletions() {
	now := s.clock.Now()
	s.regMu.Lock()
	defer s.regMu.Unlock()
	for id, rec := range s.completions {
		if !now.Before(rec.at.Add(s.upCfg.CompletionKeep)) {
			delete(s.completions, id)
		}
	}
}

// maxSessionNameRunes is the longest file name, in characters after trimming,
// that CreateSession accepts.
const maxSessionNameRunes = 200

// CreateInput describes the file a new upload session is for.
type CreateInput struct {
	// Name is the file name as the sender typed it. It must be 1 to 200
	// characters after trimming; it is sanitized before it is stored.
	Name string
	// Dir is the folder the file was picked from, "" for none.
	Dir string
	// Declared is the exact number of bytes the sender will send, 0 to
	// domain.MaxDeclaredSize. Zero is allowed: such a session completes
	// without any append.
	Declared int64
	// Conflict is ConflictRename, ConflictOverwrite or "" (rename).
	Conflict ConflictPolicy
}

// CreatedSession is the answer to CreateSession. ResumeKey is shown only here:
// it is stored hashed, so it can never be read back.
type CreatedSession struct {
	ID        domain.SessionID
	ResumeKey string
	// Offset is the Confirmed_Offset, always 0 for a new session.
	Offset    int64
	Size      int64
	ExpiresAt time.Time
}

// SessionInfo is what a client holding the resume key may learn about its
// session. It reflects the persisted record, never a half-finished append.
type SessionInfo struct {
	// Offset is the Confirmed_Offset: bytes durably written.
	Offset    int64
	Size      int64
	ExpiresAt time.Time
	// SHA256 is the lowercase hex SHA-256 of the first Offset bytes; for
	// Offset 0 it is domain.EmptySHA256.
	SHA256 string
}

// CreateSession starts a resumable upload in the actor's room.
//
// Checks run in this order and a failure at any step leaves nothing behind:
// role (ErrForbidden), input (ErrInvalidInput), permission to overwrite
// (ErrForbidden), the 16 per device / 64 per room limit (ErrTooManyUploads),
// disk space (*domain.InsufficientStorageError), then the empty partial file
// and the persisted record. The limit is claimed atomically with the registry
// entry, so concurrent requests cannot together exceed it.
//
// The session id and the resume key are two independent secrets. Only the
// hash of the key is persisted; the key is returned here and nowhere else.
func (s *Service) CreateSession(ctx context.Context, actor domain.Device, in CreateInput) (CreatedSession, error) {
	if !actor.CanUpload() {
		return CreatedSession{}, domain.ErrForbidden
	}
	if !s.uploadsConfigured() {
		return CreatedSession{}, domain.ErrNotFound
	}
	if err := s.checkRoomLimits(ctx, actor, in.Declared); err != nil {
		return CreatedSession{}, err
	}

	trimmed := strings.TrimSpace(in.Name)
	if n := utf8.RuneCountInString(trimmed); n < 1 || n > maxSessionNameRunes {
		return CreatedSession{}, fmt.Errorf("file name must be 1 to %d characters: %w", maxSessionNameRunes, domain.ErrInvalidInput)
	}
	if in.Declared < 0 || in.Declared > domain.MaxDeclaredSize {
		return CreatedSession{}, fmt.Errorf("size must be 0 to %d bytes: %w", int64(domain.MaxDeclaredSize), domain.ErrInvalidInput)
	}
	conflict := in.Conflict
	switch conflict {
	case "":
		conflict = ConflictRename
	case ConflictRename, ConflictOverwrite:
	default:
		return CreatedSession{}, fmt.Errorf("conflict must be rename or overwrite: %w", domain.ErrInvalidInput)
	}
	name := domain.SanitizeFileName(trimmed)
	dir := domain.SanitizeDir(in.Dir)

	if conflict == ConflictOverwrite {
		if err := s.checkOverwriteAllowed(ctx, actor, dir, name); err != nil {
			return CreatedSession{}, err
		}
	}

	hashState, err := emptyHashState()
	if err != nil {
		return CreatedSession{}, err
	}
	id := domain.SessionID(s.upDeps.Secrets.NewSecret())
	key := s.upDeps.Secrets.NewSecret()
	now := s.clock.Now()
	rec := domain.UploadSession{
		ID:            id,
		RoomID:        actor.RoomID,
		CreatorID:     actor.ID,
		CreatorName:   actor.Name,
		Name:          name,
		Dir:           dir,
		Declared:      in.Declared,
		Conflict:      string(conflict),
		HashState:     hashState,
		ResumeKeyHash: domain.HashResumeKey(key),
		CreatedAt:     now,
	}

	// Claim the slot first: the limit check and the registration are one
	// atomic step, and nobody can use the session yet because nobody knows
	// its key. Everything after this point is undone by abort.
	rt := newSessionRT(rec, nil)
	if err := s.registerSession(rt); err != nil {
		return CreatedSession{}, err
	}
	var (
		res       *storage.Reservation
		madePart  bool
		triedSave bool
	)
	abort := func() {
		bg := context.WithoutCancel(ctx)
		rt.mu.Lock()
		rt.dead = true
		rt.mu.Unlock()
		s.unregisterSession(rt)
		if res != nil {
			res.Release()
		}
		if madePart {
			_ = s.upDeps.Partials.Delete(bg, rec.RoomID, id)
		}
		if triedSave {
			_ = s.upDeps.Sessions.Delete(bg, rec.RoomID, id)
		}
	}

	res, err = s.upDeps.Guard.Reserve(ctx, string(id), uint64(in.Declared))
	if err != nil {
		res = nil
		abort()
		return CreatedSession{}, err
	}
	if err := s.upDeps.Partials.Create(ctx, rec.RoomID, id); err != nil {
		abort()
		return CreatedSession{}, fmt.Errorf("create partial file: %w", err)
	}
	madePart = true
	triedSave = true
	if err := s.upDeps.Sessions.Save(ctx, rec); err != nil {
		abort()
		return CreatedSession{}, fmt.Errorf("save upload session: %w", err)
	}
	rt.mu.Lock()
	rt.res = res
	rt.mu.Unlock()

	return CreatedSession{
		ID:        id,
		ResumeKey: key,
		Offset:    0,
		Size:      rec.Declared,
		ExpiresAt: rec.ExpiresAt(s.upCfg.TTL),
	}, nil
}

// checkOverwriteAllowed reports domain.ErrForbidden when a file with the same
// folder and name (compared case-insensitively) exists and actor is neither
// the owner nor the device that uploaded it.
func (s *Service) checkOverwriteAllowed(ctx context.Context, actor domain.Device, dir, name string) error {
	existing, err := s.files.ListByRoom(ctx, actor.RoomID)
	if err != nil {
		return fmt.Errorf("list files: %w", err)
	}
	for _, f := range existing {
		if sameFolder(f, dir) && strings.EqualFold(f.Name, name) {
			if !actor.CanManage() && f.UploaderID != actor.ID {
				return domain.ErrForbidden
			}
			return nil
		}
	}
	return nil
}

// QuerySession reports the persisted state of a session to the holder of its
// resume key. Like every session operation it checks the role first and then
// answers domain.ErrNotFound, and nothing else, for a session it cannot show.
func (s *Service) QuerySession(_ context.Context, actor domain.Device, id domain.SessionID, key string) (SessionInfo, error) {
	if !actor.CanUpload() {
		return SessionInfo{}, domain.ErrForbidden
	}
	if !s.uploadsConfigured() {
		return SessionInfo{}, domain.ErrNotFound
	}
	rt, err := s.lookupSession(actor.RoomID, id, key)
	if err != nil {
		return SessionInfo{}, err
	}
	rt.mu.Lock()
	rec := rt.rec
	dead := rt.dead
	rt.mu.Unlock()
	if dead {
		return SessionInfo{}, domain.ErrNotFound
	}

	sum := domain.EmptySHA256
	if rec.Offset > 0 {
		if sum, err = sumFromHashState(rec.HashState); err != nil {
			return SessionInfo{}, fmt.Errorf("session %s: %w", id, err)
		}
	}
	return SessionInfo{
		Offset:    rec.Offset,
		Size:      rec.Declared,
		ExpiresAt: rec.ExpiresAt(s.upCfg.TTL),
		SHA256:    sum,
	}, nil
}

// emptyHashState is the marshalled state of a SHA-256 that has seen no bytes.
func emptyHashState() ([]byte, error) { return marshalHashState(sha256.New()) }

// marshalHashState snapshots h into the form stored in
// domain.UploadSession.HashState. h keeps working afterwards. Every call
// returns a fresh slice, so a stored record's HashState is never modified.
func marshalHashState(h hash.Hash) ([]byte, error) {
	m, ok := h.(encoding.BinaryMarshaler)
	if !ok {
		return nil, fmt.Errorf("sha256 digest cannot be marshalled")
	}
	b, err := m.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("marshal sha256 state: %w", err)
	}
	return b, nil
}

// restoreHashState rebuilds a SHA-256 that has seen the bytes a marshalled
// state stands for, so a resumed upload never re-reads the partial file.
func restoreHashState(state []byte) (hash.Hash, error) {
	h := sha256.New()
	u, ok := h.(encoding.BinaryUnmarshaler)
	if !ok {
		return nil, fmt.Errorf("sha256 digest cannot be restored")
	}
	if err := u.UnmarshalBinary(state); err != nil {
		return nil, fmt.Errorf("restore sha256 state: %w", err)
	}
	return h, nil
}

// sumFromHashState returns the lowercase hex digest of the bytes a marshalled
// SHA-256 state has seen.
func sumFromHashState(state []byte) (string, error) {
	h, err := restoreHashState(state)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
