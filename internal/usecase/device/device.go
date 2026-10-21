// Package device implements the device list and online presence.
package device

import (
	"context"

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
