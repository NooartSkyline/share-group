// Package netinfo lists the machine's usable IPv4 addresses and every address
// the machine owns.
package netinfo

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strings"

	"lanshare/internal/domain"
	"lanshare/internal/usecase/port"
)

// Provider implements port.NetworkProvider and port.LocalAddrProvider using
// the net package. Interfaces returns only usable IPv4 LAN candidates;
// LocalAddrs returns everything, so callers can tell which addresses are the
// machine's own.
type Provider struct{}

var (
	_ port.NetworkProvider   = Provider{}
	_ port.LocalAddrProvider = Provider{}
)

func New() Provider { return Provider{} }

func (Provider) Interfaces(_ context.Context) ([]domain.NetInterface, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("list network interfaces: %w", err)
	}
	var out []domain.NetInterface
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipnet.IP.To4()
			if ip == nil || ip.IsLinkLocalUnicast() || ip.IsLoopback() {
				continue
			}
			ones, _ := ipnet.Mask.Size()
			out = append(out, domain.NetInterface{Name: ifc.Name, IP: ip.String(), Kind: classify(ifc.Name), PrefixLen: ones})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// LocalAddrs returns every IPv4 and IPv6 address of every interface, including
// loopback and interfaces that are down. IPv4-mapped IPv6 addresses are
// returned in their IPv4 form and IPv6 zones are not included.
func (Provider) LocalAddrs(_ context.Context) ([]netip.Addr, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("list network interfaces: %w", err)
	}
	var out []netip.Addr
	for _, ifc := range ifaces {
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			var raw net.IP
			switch v := a.(type) {
			case *net.IPNet:
				raw = v.IP
			case *net.IPAddr:
				raw = v.IP
			default:
				continue
			}
			addr, ok := netip.AddrFromSlice(raw)
			if !ok {
				continue
			}
			out = append(out, addr.Unmap())
		}
	}
	return out, nil
}

func classify(name string) domain.NetKind {
	for _, p := range []string{"utun", "tun", "tap", "ppp", "ipsec", "wg"} {
		if strings.HasPrefix(name, p) {
			return domain.NetVPN
		}
	}
	for _, p := range []string{"bridge", "docker", "veth", "vmnet", "vboxnet", "awdl", "llw", "anpi", "gif", "stf", "br-", "virbr"} {
		if strings.HasPrefix(name, p) {
			return domain.NetVirtual
		}
	}
	return domain.NetPhysical
}
