// Package auth implements joining a room with a PIN and resolving sessions.
package auth

import (
	"context"
	"errors"

	"lanshare/internal/domain"
	"lanshare/internal/usecase/port"
)

type Service struct {
	rooms   port.RoomRepository
	devices port.DeviceRepository
	tokens  port.TokenService
	limiter port.AttemptLimiter
	ids     port.IDGenerator
	clock   port.Clock
	events  port.EventPublisher
}

func NewService(
	rooms port.RoomRepository,
	devices port.DeviceRepository,
	tokens port.TokenService,
	limiter port.AttemptLimiter,
	ids port.IDGenerator,
	clock port.Clock,
	events port.EventPublisher,
) *Service {
	return &Service{rooms, devices, tokens, limiter, ids, clock, events}
}

type JoinInput struct {
	RoomID     domain.RoomID
	PIN        string
	DeviceName string
	Platform   string
	RemoteAddr string
	// LimiterKey groups failed attempts, typically "<client ip>|<room>".
	LimiterKey string
}

type Session struct {
	Token  string
	Device domain.Device
}

// Join admits a device to a room as a Member when the PIN is correct.
func (s *Service) Join(ctx context.Context, in JoinInput) (Session, error) {
	if left := s.limiter.Check(in.LimiterKey); left > 0 {
		return Session{}, &domain.LockedError{RetryAfter: left}
	}
	room, err := s.rooms.Get(ctx, in.RoomID)
	if err != nil {
		return Session{}, err
	}
	if !domain.ValidPINFormat(in.PIN) || !room.CheckPIN(in.PIN) {
		remaining, lockedFor := s.limiter.Fail(in.LimiterKey)
		if lockedFor > 0 {
			return Session{}, &domain.LockedError{RetryAfter: lockedFor}
		}
		return Session{}, &domain.InvalidPINError{Remaining: remaining}
	}
	s.limiter.Reset(in.LimiterKey)

	d := domain.Device{
		ID:         domain.DeviceID(s.ids.NewID()),
		RoomID:     room.ID,
		Name:       domain.NormalizeDeviceName(in.DeviceName),
		Platform:   domain.NormalizePlatform(in.Platform),
		Role:       domain.RoleMember,
		RemoteAddr: in.RemoteAddr,
		JoinedAt:   s.clock.Now(),
	}
	return s.start(ctx, d)
}

// OwnerSession starts (or resumes) the owner's session for a room.
// The transport layer is responsible for proving the caller is the owner
// (the HTTP adapter only allows loopback requests).
func (s *Service) OwnerSession(ctx context.Context, roomID domain.RoomID, name string) (Session, error) {
	room, err := s.rooms.Get(ctx, roomID)
	if err != nil {
		return Session{}, err
	}
	id := domain.DeviceID("owner-" + string(room.ID))
	d, err := s.devices.Get(ctx, id)
	if errors.Is(err, domain.ErrNotFound) {
		d = domain.Device{
			ID:       id,
			RoomID:   room.ID,
			Name:     domain.NormalizeDeviceName(name),
			Platform: "macOS",
			Role:     domain.RoleOwner,
			JoinedAt: s.clock.Now(),
		}
	} else if err != nil {
		return Session{}, err
	}
	return s.start(ctx, d)
}

func (s *Service) start(ctx context.Context, d domain.Device) (Session, error) {
	if err := s.devices.Save(ctx, d); err != nil {
		return Session{}, err
	}
	token, err := s.tokens.Issue(ctx, d.ID)
	if err != nil {
		return Session{}, err
	}
	s.events.Publish(port.Event{Type: port.EventDevices, RoomID: d.RoomID})
	return Session{Token: token, Device: d}, nil
}

// Authenticate resolves a session token to its device.
func (s *Service) Authenticate(ctx context.Context, token string) (domain.Device, error) {
	if token == "" {
		return domain.Device{}, domain.ErrUnauthorized
	}
	id, err := s.tokens.Resolve(ctx, token)
	if err != nil {
		return domain.Device{}, domain.ErrUnauthorized
	}
	d, err := s.devices.Get(ctx, id)
	if err != nil {
		return domain.Device{}, domain.ErrUnauthorized
	}
	return d, nil
}
