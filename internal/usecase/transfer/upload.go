package transfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"

	"lanshare/internal/domain"
	"lanshare/internal/usecase/storage"
)

// diskCheckEvery is how many bytes of an upload of unknown length are written
// between two looks at the free disk space.
const diskCheckEvery = 64 << 20

// UploadVerified is Upload with an optional SHA-256 from the sender.
//
// checksum is nil when the sender gave none. When it is set it must be 64 hex
// digits (else domain.ErrInvalidInput, before any byte is read) and is
// compared, ignoring case, with the SHA-256 the server computes while the body
// streams in. A difference deletes the content and returns
// *domain.ChecksumMismatchError; nothing is published. An equal checksum makes
// the file Integrity verified, no checksum makes it unverified. Either way the
// computed checksum is stored on the file.
//
// Order of work, each failure leaving nothing behind:
//
//  1. role (ErrForbidden) and checksum format;
//  2. for an overwrite, a first permission check (ErrForbidden), so a device
//     that may not replace a file is refused before it sends anything;
//  3. with a known length, a disk reservation before the first byte
//     (*domain.InsufficientStorageError); with an unknown length, a look at the
//     free space after every 64 MiB, stopping once it is below the reserve;
//  4. the body streams into the store through a SHA-256, never read twice;
//  5. inside the room lock, after the content is stored, the final name is
//     chosen against the files of that moment, an overwrite is permitted
//     again, the metadata is saved, a replaced file is deleted
//     (file.deleted) and file.added is published.
//
// Without ConfigureUploads there is no disk guard, and the rest still applies.
func (s *Service) UploadVerified(ctx context.Context, actor domain.Device, in UploadInput, checksum *string) (domain.File, error) {
	if !actor.CanUpload() {
		return domain.File{}, domain.ErrForbidden
	}
	if err := s.checkRoomLimits(ctx, actor, in.Size); err != nil {
		return domain.File{}, err
	}
	var supplied string
	if checksum != nil {
		var err error
		if supplied, err = domain.NormalizeChecksum(*checksum); err != nil {
			return domain.File{}, fmt.Errorf("checksum: %w", err)
		}
	}
	name := domain.SanitizeFileName(in.Name)
	dir := domain.SanitizeDir(in.Dir)

	if in.Conflict == ConflictOverwrite {
		existing, err := s.files.ListByRoom(ctx, actor.RoomID)
		if err != nil {
			return domain.File{}, fmt.Errorf("list files: %w", err)
		}
		if _, err := resolveTarget(existing, actor, dir, name, in.Conflict); err != nil {
			return domain.File{}, err
		}
	}

	id := domain.FileID(s.ids.NewID())
	bg := context.WithoutCancel(ctx)
	guard := s.upDeps.Guard
	var res *storage.Reservation
	if guard != nil && in.Size >= 0 {
		var err error
		if res, err = guard.Reserve(ctx, "upload:"+string(id), uint64(in.Size)); err != nil {
			return domain.File{}, err
		}
		defer res.Release()
	}
	defer s.activity().Begin()()

	t := s.newTracker(actor, string(id), name, DirectionUpload, in.Size)
	h := sha256.New()
	var body io.Reader = &hashingReader{r: in.Body, h: h}
	var sp *spaceReader
	if guard != nil {
		sp = &spaceReader{r: body, ctx: ctx, guard: guard, res: res, size: in.Size, next: diskCheckEvery}
		body = sp
	}
	written, err := s.store.Put(ctx, actor.RoomID, id, &countingReader{r: body, t: t})
	if err == nil {
		if err = ctx.Err(); err != nil {
			_ = s.store.Delete(bg, actor.RoomID, id)
		}
	}
	if err != nil {
		t.finish(StateFailed)
		switch {
		case sp != nil && sp.stop != nil:
			return domain.File{}, sp.stop
		case guard != nil && errors.Is(err, domain.ErrDiskFull):
			return domain.File{}, diskFullError(bg, guard, in.Size, sp.total)
		}
		return domain.File{}, err
	}

	computed := hex.EncodeToString(h.Sum(nil))
	if checksum != nil && supplied != computed {
		_ = s.store.Delete(bg, actor.RoomID, id)
		t.finish(StateFailed)
		return domain.File{}, &domain.ChecksumMismatchError{Supplied: supplied, Computed: computed}
	}
	integrity := domain.IntegrityUnverified
	if checksum != nil {
		integrity = domain.IntegrityVerified
	}

	f, err := s.publishStored(bg, actor, domain.File{
		ID:           id,
		RoomID:       actor.RoomID,
		Dir:          dir,
		Name:         name,
		Size:         written,
		UploaderID:   actor.ID,
		UploaderName: actor.Name,
		CreatedAt:    s.clock.Now(),
		SHA256:       computed,
		Integrity:    integrity,
	}, in.Conflict)
	if err != nil {
		t.finish(StateFailed)
		return domain.File{}, err
	}
	f = s.convertPublishedHEIC(bg, f)
	t.finish(StateDone)
	return f, nil
}

// diskFullError is the error for a write that hit a full disk: the bytes still
// to come plus the reserve, against the free space read now. A length that is
// unknown counts as zero bytes to come; an unreadable free space as none.
func diskFullError(ctx context.Context, g *storage.Guard, size, written int64) error {
	snap := g.Snapshot(ctx)
	var remaining uint64
	if size > written {
		remaining = uint64(size - written)
	}
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

// hashingReader feeds every byte it hands out into h, in order.
type hashingReader struct {
	r io.Reader
	h hash.Hash
}

func (r *hashingReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	if n > 0 {
		r.h.Write(p[:n]) // hash.Hash.Write never fails
	}
	return n, err
}

// spaceReader watches the disk while an upload streams in. Every 64 MiB it
// either lowers the upload's reservation to the bytes still to come (length
// known) or, for an upload of unknown length, reads the free space and ends
// the stream with *domain.InsufficientStorageError once that is below the
// reserve. The first such error is kept in stop and repeated on every later
// Read.
type spaceReader struct {
	r     io.Reader
	ctx   context.Context
	guard *storage.Guard
	res   *storage.Reservation // nil when the length is unknown
	size  int64                // declared length, -1 when unknown

	total int64 // bytes read so far
	next  int64 // total at which to look at the disk next
	stop  error
}

func (r *spaceReader) Read(p []byte) (int, error) {
	if r.stop != nil {
		return 0, r.stop
	}
	n, err := r.r.Read(p)
	r.total += int64(n)
	if r.total < r.next {
		return n, err
	}
	r.next = r.total - r.total%diskCheckEvery + diskCheckEvery
	if r.res != nil {
		r.res.Update(uint64(max(r.size-r.total, 0)))
		return n, err
	}
	if snap := r.guard.Snapshot(r.ctx); snap.FreeKnown && snap.Free < snap.MinFree {
		r.stop = &domain.InsufficientStorageError{Required: snap.MinFree, Available: snap.Free}
		return 0, r.stop
	}
	return n, err
}
