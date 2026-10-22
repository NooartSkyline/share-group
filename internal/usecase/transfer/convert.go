package transfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"

	"lanshare/internal/domain"
)

// convertPublishedHEIC replaces a stored HEIC/HEIF with a JPEG in place.
// A conversion failure leaves the original file as it was.
func (s *Service) convertPublishedHEIC(ctx context.Context, f domain.File) domain.File {
	if s.heic == nil || !domain.IsHEIC(f.Name) {
		return f
	}
	src, err := s.store.Open(ctx, f.RoomID, f.ID)
	if err != nil {
		return f
	}
	jpegName, body, _, err := s.heic.Convert(ctx, f.Name, src)
	_ = src.Close()
	if err != nil {
		return f
	}
	defer body.Close()

	h := sha256.New()
	written, err := s.store.Put(ctx, f.RoomID, f.ID, io.TeeReader(body, h))
	if err != nil || written <= 0 {
		return f
	}
	f.Name = jpegName
	f.Size = written
	f.SHA256 = hex.EncodeToString(h.Sum(nil))
	f.Integrity = domain.IntegrityUnverified
	if err := s.files.Save(ctx, f); err != nil {
		return f
	}
	return f
}
