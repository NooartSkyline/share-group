package transfer

import (
	"context"

	"lanshare/internal/domain"
)

// checkRoomLimits refuses an upload that would exceed the room quota or the
// guest per-file cap. extra is the declared size; a negative extra (unknown
// length) is allowed as long as the room is not already at the quota.
func (s *Service) checkRoomLimits(ctx context.Context, actor domain.Device, extra int64) error {
	if s.rooms == nil {
		return nil
	}
	room, err := s.rooms.Get(ctx, actor.RoomID)
	if err != nil {
		return err
	}
	if actor.Role == domain.RoleUploader && room.GuestMax > 0 && extra >= 0 && extra > room.GuestMax {
		return &domain.SizeExceededError{}
	}
	if room.QuotaBytes <= 0 {
		return nil
	}
	files, err := s.files.ListByRoom(ctx, actor.RoomID)
	if err != nil {
		return err
	}
	var used int64
	for _, f := range files {
		used += f.Size
	}
	need := extra
	if need < 0 {
		need = 0
	}
	if used+need > room.QuotaBytes {
		avail := room.QuotaBytes - used
		if avail < 0 {
			avail = 0
		}
		req := need
		if req < 0 {
			req = 0
		}
		return &domain.InsufficientStorageError{Required: uint64(req), Available: uint64(avail)}
	}
	return nil
}

func (s *Service) sweepExpiredFiles(ctx context.Context) {
	if s.rooms == nil {
		return
	}
	rooms, err := s.rooms.List(ctx)
	if err != nil {
		return
	}
	now := s.clock.Now()
	for _, r := range rooms {
		if r.FileTTL <= 0 {
			continue
		}
		files, err := s.files.ListByRoom(ctx, r.ID)
		if err != nil {
			continue
		}
		cutoff := now.Add(-r.FileTTL)
		for _, f := range files {
			if !f.CreatedAt.After(cutoff) {
				s.remove(ctx, r.ID, f.ID)
			}
		}
	}
}
