// Package device implements the device list, presence and Owner actions.
package device

import (
	"context"
	"fmt"

	"lanshare/internal/domain"
	"lanshare/internal/usecase/port"
)

type Service struct {
	devices port.DeviceRepository
	events  port.EventPublisher
}

func NewService(devices port.DeviceRepository, events port.EventPublisher) *Service {
	return &Service{devices: devices, events: events}
}

// List returns the devices of the actor's room only.
func (s *Service) List(ctx context.Context, actor domain.Device) ([]domain.Device, error) {
	return s.devices.ListByRoom(ctx, actor.RoomID)
}

// SetPresence marks a device online or offline and tells its room.
func (s *Service) SetPresence(ctx context.Context, id domain.DeviceID, online bool) error {
	d, err := s.devices.Get(ctx, id)
	if err != nil {
		return err
	}
	if d.Online == online {
		return nil
	}
	d.Online = online
	if err := s.devices.Save(ctx, d); err != nil {
		return err
	}
	s.events.Publish(port.Event{Type: port.EventDevices, RoomID: d.RoomID})
	return nil
}

// Approve lets a pending device use the room with its current role.
func (s *Service) Approve(ctx context.Context, actor domain.Device, id domain.DeviceID) (domain.Device, error) {
	d, err := s.lookup(ctx, actor, id)
	if err != nil {
		return domain.Device{}, err
	}
	if d.Role == domain.RoleOwner {
		return domain.Device{}, domain.ErrForbidden
	}
	d.Pending = false
	if err := s.devices.Save(ctx, d); err != nil {
		return domain.Device{}, err
	}
	s.events.Publish(port.Event{Type: port.EventDevices, RoomID: d.RoomID})
	return d, nil
}

// Reject removes a device from the room. The next request with its token is unauthorized.
func (s *Service) Reject(ctx context.Context, actor domain.Device, id domain.DeviceID) error {
	d, err := s.lookup(ctx, actor, id)
	if err != nil {
		return err
	}
	if d.Role == domain.RoleOwner {
		return domain.ErrForbidden
	}
	if err := s.devices.Delete(ctx, d.ID); err != nil {
		return err
	}
	s.events.Publish(port.Event{Type: port.EventDevices, RoomID: d.RoomID})
	return nil
}

// SetRole changes a non-owner device's role. Owner cannot be assigned or demoted.
func (s *Service) SetRole(ctx context.Context, actor domain.Device, id domain.DeviceID, role domain.Role) (domain.Device, error) {
	if role == domain.RoleOwner || !role.Valid() {
		return domain.Device{}, fmt.Errorf("role: %w", domain.ErrInvalidInput)
	}
	d, err := s.lookup(ctx, actor, id)
	if err != nil {
		return domain.Device{}, err
	}
	if d.Role == domain.RoleOwner {
		return domain.Device{}, domain.ErrForbidden
	}
	d.Role = role
	if err := s.devices.Save(ctx, d); err != nil {
		return domain.Device{}, err
	}
	s.events.Publish(port.Event{Type: port.EventDevices, RoomID: d.RoomID})
	return d, nil
}

// Rename changes the display name of a device in the actor's room.
func (s *Service) Rename(ctx context.Context, actor domain.Device, id domain.DeviceID, name string) (domain.Device, error) {
	d, err := s.lookup(ctx, actor, id)
	if err != nil {
		return domain.Device{}, err
	}
	d.Name = domain.NormalizeDeviceName(name)
	if err := s.devices.Save(ctx, d); err != nil {
		return domain.Device{}, err
	}
	s.events.Publish(port.Event{Type: port.EventDevices, RoomID: d.RoomID})
	return d, nil
}

func (s *Service) lookup(ctx context.Context, actor domain.Device, id domain.DeviceID) (domain.Device, error) {
	if !actor.CanManage() {
		return domain.Device{}, domain.ErrForbidden
	}
	d, err := s.devices.Get(ctx, id)
	if err != nil {
		return domain.Device{}, domain.ErrNotFound
	}
	if d.RoomID != actor.RoomID {
		return domain.Device{}, domain.ErrNotFound
	}
	return d, nil
}
