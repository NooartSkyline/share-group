// Package room implements room lookup, creation and PIN management.
package room

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"lanshare/internal/domain"
	"lanshare/internal/usecase/port"
)

type Service struct {
	rooms port.RoomRepository
	clock port.Clock
	rnd   io.Reader // randomness for PINs and IDs; nil means crypto/rand
}

func NewService(rooms port.RoomRepository, clock port.Clock, rnd io.Reader) *Service {
	return &Service{rooms: rooms, clock: clock, rnd: rnd}
}

// EnsureDefault creates the room with a fresh PIN when it does not exist yet.
func (s *Service) EnsureDefault(ctx context.Context, id domain.RoomID, name string) (domain.Room, error) {
	r, err := s.rooms.Get(ctx, id)
	if err == nil {
		return r, nil
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return domain.Room{}, err
	}
	if !domain.ValidRoomID(string(id)) {
		return domain.Room{}, fmt.Errorf("room id: %w", domain.ErrInvalidInput)
	}
	pin, err := domain.NewPIN(s.rnd)
	if err != nil {
		return domain.Room{}, err
	}
	r = domain.Room{ID: id, Name: domain.NormalizeRoomName(name), PIN: pin, CreatedAt: s.clock.Now()}
	if err := s.rooms.Save(ctx, r); err != nil {
		return domain.Room{}, err
	}
	return r, nil
}

// Create makes a new room. Only an Owner may do that; the new room is empty
// and has its own PIN.
func (s *Service) Create(ctx context.Context, actor domain.Device, name string) (domain.Room, error) {
	if !actor.CanManage() {
		return domain.Room{}, domain.ErrForbidden
	}
	id, err := domain.NewRoomID(s.rnd)
	if err != nil {
		return domain.Room{}, err
	}
	pin, err := domain.NewPIN(s.rnd)
	if err != nil {
		return domain.Room{}, err
	}
	r := domain.Room{
		ID:        id,
		Name:      domain.NormalizeRoomName(name),
		PIN:       pin,
		CreatedAt: s.clock.Now(),
	}
	if err := s.rooms.Save(ctx, r); err != nil {
		return domain.Room{}, err
	}
	return r, nil
}

// ListForOwner returns every room on this server. Rooms are not secret from
// the Mac Owner; other roles cannot list them.
func (s *Service) ListForOwner(ctx context.Context, actor domain.Device) ([]domain.Room, error) {
	if !actor.CanManage() {
		return nil, domain.ErrForbidden
	}
	return s.rooms.List(ctx)
}

// Get returns a room without its secrets being checked; callers must not
// expose the PIN unless the actor may manage the room (see GetForManager).
func (s *Service) Get(ctx context.Context, id domain.RoomID) (domain.Room, error) {
	return s.rooms.Get(ctx, id)
}

// GetForManager returns the actor's room including its PIN.
func (s *Service) GetForManager(ctx context.Context, actor domain.Device) (domain.Room, error) {
	if !actor.CanManage() {
		return domain.Room{}, domain.ErrForbidden
	}
	return s.rooms.Get(ctx, actor.RoomID)
}

// RotatePIN replaces the PIN of the actor's room.
func (s *Service) RotatePIN(ctx context.Context, actor domain.Device) (domain.Room, error) {
	if !actor.CanManage() {
		return domain.Room{}, domain.ErrForbidden
	}
	r, err := s.rooms.Get(ctx, actor.RoomID)
	if err != nil {
		return domain.Room{}, err
	}
	pin, err := domain.NewPIN(s.rnd)
	if err != nil {
		return domain.Room{}, err
	}
	r.PIN = pin
	if err := s.rooms.Save(ctx, r); err != nil {
		return domain.Room{}, err
	}
	return r, nil
}

// Settings is the subset of a room the Owner may change.
type Settings struct {
	Name            string
	QuotaBytes      int64
	FileTTL         time.Duration
	RequireApproval bool
}

// UpdateSettings writes the Owner's choices onto their current room.
func (s *Service) UpdateSettings(ctx context.Context, actor domain.Device, in Settings) (domain.Room, error) {
	if !actor.CanManage() {
		return domain.Room{}, domain.ErrForbidden
	}
	if in.QuotaBytes < 0 {
		return domain.Room{}, fmt.Errorf("quota: %w", domain.ErrInvalidInput)
	}
	if in.FileTTL < 0 {
		return domain.Room{}, fmt.Errorf("file ttl: %w", domain.ErrInvalidInput)
	}
	r, err := s.rooms.Get(ctx, actor.RoomID)
	if err != nil {
		return domain.Room{}, err
	}
	r.Name = domain.NormalizeRoomName(in.Name)
	r.QuotaBytes = in.QuotaBytes
	r.FileTTL = in.FileTTL
	r.RequireApproval = in.RequireApproval
	if err := s.rooms.Save(ctx, r); err != nil {
		return domain.Room{}, err
	}
	return r, nil
}

// RotateGuest issues a new upload-only guest link that expires after ttl.
// maxBytes is the per-file cap for guests; 0 means no extra cap.
func (s *Service) RotateGuest(ctx context.Context, actor domain.Device, ttl time.Duration, maxBytes int64) (domain.Room, error) {
	if !actor.CanManage() {
		return domain.Room{}, domain.ErrForbidden
	}
	if ttl < time.Minute {
		return domain.Room{}, fmt.Errorf("guest ttl must be at least 1m: %w", domain.ErrInvalidInput)
	}
	if maxBytes < 0 {
		return domain.Room{}, fmt.Errorf("guest max: %w", domain.ErrInvalidInput)
	}
	r, err := s.rooms.Get(ctx, actor.RoomID)
	if err != nil {
		return domain.Room{}, err
	}
	key, err := domain.NewGuestKey(s.rnd)
	if err != nil {
		return domain.Room{}, err
	}
	r.GuestKey = key
	r.GuestUntil = s.clock.Now().Add(ttl)
	r.GuestMax = maxBytes
	if err := s.rooms.Save(ctx, r); err != nil {
		return domain.Room{}, err
	}
	return r, nil
}

// ClearGuest disables the guest link.
func (s *Service) ClearGuest(ctx context.Context, actor domain.Device) (domain.Room, error) {
	if !actor.CanManage() {
		return domain.Room{}, domain.ErrForbidden
	}
	r, err := s.rooms.Get(ctx, actor.RoomID)
	if err != nil {
		return domain.Room{}, err
	}
	r.GuestKey = ""
	r.GuestUntil = time.Time{}
	r.GuestMax = 0
	if err := s.rooms.Save(ctx, r); err != nil {
		return domain.Room{}, err
	}
	return r, nil
}

// LookupGuest returns the room for an active guest key. Unknown, expired or
// empty keys all look like not found.
func (s *Service) LookupGuest(ctx context.Context, key string) (domain.Room, error) {
	if len(key) != 16 {
		return domain.Room{}, domain.ErrNotFound
	}
	for i := 0; i < len(key); i++ {
		c := key[i]
		if c >= '0' && c <= '9' || c >= 'a' && c <= 'f' {
			continue
		}
		return domain.Room{}, domain.ErrNotFound
	}
	list, err := s.rooms.List(ctx)
	if err != nil {
		return domain.Room{}, err
	}
	now := s.clock.Now()
	var found domain.Room
	ok := false
	for _, r := range list {
		if !r.GuestActive(now) {
			continue
		}
		if r.GuestKey == key {
			found = r
			ok = true
		}
	}
	if !ok {
		return domain.Room{}, domain.ErrNotFound
	}
	return found, nil
}
