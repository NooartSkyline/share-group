// Package netinfo lists the machine's usable IPv4 addresses.
package netinfo

import (
	"context"
	"net"
	"sort"
	"strings"

	"lanshare/internal/domain"
)

// Provider implements port.NetworkProvider using the net package.
type Provider struct{}

func New() Provider { return Provider{} }

func (Provider) Interfaces(_ context.Context) ([]domain.NetInterface, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
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
			out = append(out, domain.NetInterface{Name: ifc.Name, IP: ip.String(), Kind: classify(ifc.Name)})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
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
