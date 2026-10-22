package network

import (
	"context"
	"time"

	"lanshare/internal/domain"
)

const (
	cableCommandTimeout = 3 * time.Second

	commandNetworkSetup = "/usr/sbin/networksetup"
	commandIOReg        = "/usr/sbin/ioreg"

	// CableIdle: nothing plugged in, no USB network.
	CableIdle = "idle"
	// CablePlugged: a phone is on USB but tethering / hotspot is not up yet.
	CablePlugged = "plugged"
	// CableReady: a USB network address is up; the phone can open the URL.
	CableReady = "ready"
)

// CableLink is a USB interface the phone can use to reach this Mac.
type CableLink struct {
	Name   string // BSD name, e.g. en5
	Label  string
	IP     string
	Device string // iphone | ipad | android | usb
	URL    string
}

// CablePhone is a USB device that looks like a phone, even before tethering.
type CablePhone struct {
	Device string
	Name   string
}

// CableStatus is what the Owner panel shows for USB transfers.
type CableStatus struct {
	State  string
	Links  []CableLink
	Phones []CablePhone
}

// Cable reports USB tethering / Personal Hotspot status for the Owner.
func (s *Service) Cable(ctx context.Context, actor domain.Device) (CableStatus, error) {
	if !actor.CanManage() {
		return CableStatus{}, domain.ErrForbidden
	}
	all, err := s.listed(ctx)
	if err != nil {
		all = nil // still report a plugged phone from USB even if interface listing failed
	}
	ports := s.hardwarePorts(ctx)
	st := CableStatus{
		State:  CableIdle,
		Links:  make([]CableLink, 0),
		Phones: s.usbPhones(ctx),
	}
	for _, n := range all {
		if n.Kind != domain.NetUSB {
			continue
		}
		label := n.Label
		if label == "" {
			label = n.Name
		}
		device := cableDeviceOf(n, ports[n.Name])
		if device == "" {
			device = domain.CableGeneric
		}
		st.Links = append(st.Links, CableLink{
			Name: n.Name, Label: label, IP: n.IP, Device: device,
			URL: s.URLOn(n, actor.RoomID),
		})
	}
	switch {
	case len(st.Links) > 0:
		st.State = CableReady
	case len(st.Phones) > 0:
		st.State = CablePlugged
	}
	return st, nil
}

func (s *Service) hardwarePorts(ctx context.Context) map[string]string {
	out := s.runShort(ctx, commandNetworkSetup, "-listallhardwareports")
	if out == nil {
		return map[string]string{}
	}
	return parseHardwarePortNames(out)
}

func (s *Service) usbPhones(ctx context.Context) []CablePhone {
	out := s.runShort(ctx, commandIOReg, "-r", "-c", "IOUSBHostDevice", "-l", "-d", "1", "-w", "0")
	if out == nil {
		return nil
	}
	return parseUSBPhones(out)
}

func (s *Service) runShort(ctx context.Context, name string, args ...string) []byte {
	if s.runner == nil {
		return nil
	}
	cctx, cancel := context.WithTimeout(ctx, cableCommandTimeout)
	defer cancel()
	out, err := s.runner.Run(cctx, name, args...)
	if err != nil {
		return nil
	}
	return out
}
