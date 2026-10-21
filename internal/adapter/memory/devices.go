// Package memory holds in-memory adapters for state that does not need to
// survive a restart: devices, session tokens and PIN attempt counters.
package memory

import (
	"context"
	"sort"
	"sync"

	"lanshare/internal/domain"
)

// DeviceRepository implements port.DeviceRepository.
type DeviceRepository struct {
	mu      sync.RWMutex
	devices map[domain.DeviceID]domain.Device
}

func NewDeviceRepository() *DeviceRepository {
	return &DeviceRepository{devices: make(map[domain.DeviceID]domain.Device)}
}

func (r *DeviceRepository) Save(_ context.Context, d domain.Device) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.devices[d.ID] = d
	return nil
}

func (r *DeviceRepository) Get(_ context.Context, id domain.DeviceID) (domain.Device, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, ok := r.devices[id]
	if !ok {
		return domain.Device{}, domain.ErrNotFound
	}
	return d, nil
}

func (r *DeviceRepository) ListByRoom(_ context.Context, room domain.RoomID) ([]domain.Device, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []domain.Device
	for _, d := range r.devices {
		if d.RoomID == room {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].JoinedAt.Equal(out[j].JoinedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].JoinedAt.Before(out[j].JoinedAt)
	})
	return out, nil
}
