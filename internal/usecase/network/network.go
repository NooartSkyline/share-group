// Package network chooses which local interface is advertised in the join
// URL and QR code, so other devices are pointed at an address they can reach.
package network

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"sync"

	"lanshare/internal/domain"
	"lanshare/internal/usecase/port"
)

type Service struct {
	provider port.NetworkProvider
	port     int

	mu       sync.RWMutex
	selected string // interface name; empty means automatic
}

// NewService builds the service. preferred may be empty (automatic choice).
func NewService(provider port.NetworkProvider, port int, preferred string) *Service {
	return &Service{provider: provider, port: port, selected: preferred}
}

// Option is an interface the owner can pick.
type Option struct {
	domain.NetInterface
	Selected    bool
	Recommended bool
}

// List returns all usable interfaces and marks the active and recommended ones.
func (s *Service) List(ctx context.Context, actor domain.Device) ([]Option, error) {
	if !actor.Role.CanManage() {
		return nil, domain.ErrForbidden
	}
	all, err := s.provider.Interfaces(ctx)
	if err != nil {
		return nil, err
	}
	active, _ := s.pick(all)
	rec, _ := pickAuto(all)
	out := make([]Option, 0, len(all))
	for _, n := range all {
		out = append(out, Option{
			NetInterface: n,
			Selected:     n.Name == active.Name && n.IP == active.IP,
			Recommended:  n.Name == rec.Name && n.IP == rec.IP,
		})
	}
	return out, nil
}

// Select makes name the advertised interface.
func (s *Service) Select(ctx context.Context, actor domain.Device, name string) error {
	if !actor.Role.CanManage() {
		return domain.ErrForbidden
	}
	all, err := s.provider.Interfaces(ctx)
	if err != nil {
		return err
	}
	for _, n := range all {
		if n.Name == name {
			s.mu.Lock()
			s.selected = name
			s.mu.Unlock()
			return nil
		}
	}
	return fmt.Errorf("%w: unknown interface %q", domain.ErrInvalidInput, name)
}

// Active returns the interface currently advertised.
func (s *Service) Active(ctx context.Context) (domain.NetInterface, error) {
	all, err := s.provider.Interfaces(ctx)
	if err != nil {
		return domain.NetInterface{}, err
	}
	return s.pick(all)
}

// JoinURL is the address other devices should open for the given room.
func (s *Service) JoinURL(ctx context.Context, room domain.RoomID) (string, error) {
	n, err := s.Active(ctx)
	if err != nil {
		return "", err
	}
	host := net.JoinHostPort(n.IP, strconv.Itoa(s.port))
	return fmt.Sprintf("http://%s/?r=%s", host, room), nil
}

func (s *Service) pick(all []domain.NetInterface) (domain.NetInterface, error) {
	s.mu.RLock()
	want := s.selected
	s.mu.RUnlock()
	if want != "" {
		for _, n := range all {
			if n.Name == want {
				return n, nil
			}
		}
	}
	return pickAuto(all)
}

// pickAuto prefers a physical interface (en0 first), then anything else.
func pickAuto(all []domain.NetInterface) (domain.NetInterface, error) {
	var firstPhysical, any *domain.NetInterface
	for i := range all {
		n := &all[i]
		if any == nil {
			any = n
		}
		if n.Kind != domain.NetPhysical {
			continue
		}
		if n.Name == "en0" {
			return *n, nil
		}
		if firstPhysical == nil {
			firstPhysical = n
		}
	}
	switch {
	case firstPhysical != nil:
		return *firstPhysical, nil
	case any != nil:
		return *any, nil
	}
	return domain.NetInterface{}, domain.ErrNoNetwork
}
