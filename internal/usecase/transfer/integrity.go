package transfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"

	"lanshare/internal/domain"
)

const (
	// checksumWait is how long a lazy checksum request may queue behind the
	// running one before it is rejected with ErrChecksumBusy.
	checksumWait = 60 * time.Second
	// checksumBuf is the only file content held in memory while hashing.
	checksumBuf = 1 << 20
)

// ChecksumInfo is the answer to FileChecksum.
type ChecksumInfo struct {
	// SHA256 is the lowercase hex digest of the stored content.
	SHA256 string
	// Integrity says how far SHA256 was checked. A digest the server computed
	// afterwards for an old file has no reference from the sender, so it
	// stays unknown.
	Integrity domain.Integrity
}

// FileChecksum returns the SHA-256 of a file, computing it on first request
// for files stored before checksums existed.
//
// Only one such computation runs at a time; others queue for up to
// checksumWait and then fail with ErrChecksumBusy. The digest is stored only
// when the file is still in the room afterwards (SetChecksum reports
// ErrNotFound for a file that was deleted or replaced meanwhile), and the
// stored Integrity is never changed here. A read error is ErrChecksumFailed.
// Lookups are scoped to the actor's room, so other rooms' files are
// ErrNotFound.
func (s *Service) FileChecksum(ctx context.Context, actor domain.Device, id domain.FileID) (ChecksumInfo, error) {
	if !actor.CanDownload() {
		return ChecksumInfo{}, domain.ErrForbidden
	}
	f, err := s.files.Get(ctx, actor.RoomID, id)
	if err != nil {
		return ChecksumInfo{}, err
	}
	if f.SHA256 != "" {
		return checksumInfo(f), nil
	}

	if err := s.acquireChecksumSlot(ctx); err != nil {
		return ChecksumInfo{}, err
	}
	defer func() { <-s.sumSem }()

	// A request that was queued ahead of us may have stored the digest, or
	// the file may be gone by now; either way no hashing is needed.
	f, err = s.files.Get(ctx, actor.RoomID, id)
	if err != nil {
		return ChecksumInfo{}, err
	}
	if f.SHA256 != "" {
		return checksumInfo(f), nil
	}

	sum, err := s.hashStored(ctx, actor.RoomID, id)
	if err != nil {
		return ChecksumInfo{}, err
	}
	if err := s.files.SetChecksum(ctx, actor.RoomID, id, sum); err != nil {
		return ChecksumInfo{}, fmt.Errorf("store checksum: %w", err)
	}
	f.SHA256 = sum
	return checksumInfo(f), nil
}

func checksumInfo(f domain.File) ChecksumInfo {
	return ChecksumInfo{SHA256: f.SHA256, Integrity: f.IntegrityOrUnknown()}
}

// acquireChecksumSlot takes the single computation slot, waiting at most
// checksumWait. The caller releases it by receiving from s.sumSem.
func (s *Service) acquireChecksumSlot(ctx context.Context) error {
	select {
	case s.sumSem <- struct{}{}:
		return nil
	default:
	}
	t := time.NewTimer(checksumWait)
	defer t.Stop()
	select {
	case s.sumSem <- struct{}{}:
		return nil
	case <-t.C:
		return domain.ErrChecksumBusy
	case <-ctx.Done():
		return ctx.Err()
	}
}

// hashStored streams the stored content through SHA-256 using one 1 MiB
// buffer. A file that vanishes is ErrNotFound; any other failure is
// ErrChecksumFailed, except cancellation, which is reported as is.
func (s *Service) hashStored(ctx context.Context, room domain.RoomID, id domain.FileID) (string, error) {
	src, err := s.store.Open(ctx, room, id)
	if err != nil {
		return "", checksumReadError(ctx, "open", err)
	}
	defer src.Close()

	h := sha256.New()
	if _, err := io.CopyBuffer(h, &ctxReader{ctx: ctx, r: src}, make([]byte, checksumBuf)); err != nil {
		return "", checksumReadError(ctx, "read", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func checksumReadError(ctx context.Context, op string, err error) error {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return fmt.Errorf("%s stored file: %w", op, domain.ErrNotFound)
	case ctx.Err() != nil:
		return fmt.Errorf("%s stored file: %w", op, ctx.Err())
	default:
		return fmt.Errorf("%s stored file: %w: %v", op, domain.ErrChecksumFailed, err)
	}
}

// ctxReader stops a long read as soon as ctx is done.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c *ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}
