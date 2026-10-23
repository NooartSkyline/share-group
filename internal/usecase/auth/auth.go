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
// A device that already joined this room with the same name and platform is
// reused (remembered). When the room requires approval, a new device stays
// Pending until the Owner accepts it.
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

	name := domain.NormalizeDeviceName(in.DeviceName)
	platform := domain.NormalizePlatform(in.Platform)
	if d, ok := s.remembered(ctx, room.ID, name, platform); ok {
		d.RemoteAddr = in.RemoteAddr
		return s.start(ctx, d)
	}

	d := domain.Device{
		ID:         domain.DeviceID(s.ids.NewID()),
		RoomID:     room.ID,
		Name:       name,
		Platform:   platform,
		Role:       domain.RoleMember,
		RemoteAddr: in.RemoteAddr,
		JoinedAt:   s.clock.Now(),
		Pending:    room.RequireApproval,
	}
	return s.start(ctx, d)
}

// JoinGuest admits a device as Uploader through an active guest link. Wrong
// or expired keys look like not found. Guest devices skip PIN and approval.
func (s *Service) JoinGuest(ctx context.Context, key, deviceName, platform, remoteAddr string) (Session, error) {
	room, err := s.lookupGuest(ctx, key)
	if err != nil {
		return Session{}, err
	}
	d := domain.Device{
		ID:         domain.DeviceID(s.ids.NewID()),
		RoomID:     room.ID,
		Name:       domain.NormalizeDeviceName(deviceName),
		Platform:   domain.NormalizePlatform(platform),
		Role:       domain.RoleUploader,
		RemoteAddr: remoteAddr,
		JoinedAt:   s.clock.Now(),
	}
	return s.start(ctx, d)
}

func (s *Service) lookupGuest(ctx context.Context, key string) (domain.Room, error) {
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
		if r.GuestActive(now) && r.GuestKey == key {
			found = r
			ok = true
		}
	}
	if !ok {
		return domain.Room{}, domain.ErrNotFound
	}
	return found, nil
}

func (s *Service) remembered(ctx context.Context, room domain.RoomID, name, platform string) (domain.Device, bool) {
	list, err := s.devices.ListByRoom(ctx, room)
	if err != nil {
		return domain.Device{}, false
	}
	for _, d := range list {
		if d.Role == domain.RoleOwner {
			continue
		}
		if d.Name == name && d.Platform == platform {
			return d, true
		}
	}
	return domain.Device{}, false
}

// OwnerSession starts (or resumes) the owner's session for a room.
// The transport layer is responsible for proving the caller is the owner
// (the HTTP adapter only allows loopback requests).
func (s *Service) OwnerSession(ctx context.Context, roomID domain.RoomID, name, platform string) (Session, error) {
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
			Platform: domain.NormalizePlatform(platform),
			Role:     domain.RoleOwner,
			JoinedAt: s.clock.Now(),
		}
	} else if err != nil {
		return Session{}, err
	} else {
		d.Name = domain.NormalizeDeviceName(name)
		d.Platform = domain.NormalizePlatform(platform)
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
