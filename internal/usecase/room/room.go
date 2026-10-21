// Package room implements room lookup and PIN management.
package room

import (
	"context"
	"errors"
	"io"

	"lanshare/internal/domain"
	"lanshare/internal/usecase/port"
)

type Service struct {
	rooms port.RoomRepository
	clock port.Clock
	rnd   io.Reader // randomness for PINs; nil means crypto/rand
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
	pin, err := domain.NewPIN(s.rnd)
	if err != nil {
		return domain.Room{}, err
	}
	r = domain.Room{ID: id, Name: name, PIN: pin, CreatedAt: s.clock.Now()}
	if err := s.rooms.Save(ctx, r); err != nil {
		return domain.Room{}, err
	}
	return r, nil
}

// Get returns a room without its secrets being checked; callers must not
// expose the PIN unless the actor may manage the room (see GetForManager).
func (s *Service) Get(ctx context.Context, id domain.RoomID) (domain.Room, error) {
	return s.rooms.Get(ctx, id)
}

// GetForManager returns the actor's room including its PIN.
func (s *Service) GetForManager(ctx context.Context, actor domain.Device) (domain.Room, error) {
	if !actor.Role.CanManage() {
		return domain.Room{}, domain.ErrForbidden
	}
	return s.rooms.Get(ctx, actor.RoomID)
}

// RotatePIN replaces the PIN of the actor's room.
func (s *Service) RotatePIN(ctx context.Context, actor domain.Device) (domain.Room, error) {
	if !actor.Role.CanManage() {
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
