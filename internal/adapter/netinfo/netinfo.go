// Package netinfo lists the machine's usable IPv4 addresses and every address
// the machine owns.
package netinfo

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"lanshare/internal/domain"
	"lanshare/internal/usecase/port"
)

// Provider implements port.NetworkProvider and port.LocalAddrProvider using
// the net package. Interfaces returns only usable IPv4 LAN candidates;
// LocalAddrs returns everything, so callers can tell which addresses are the
// machine's own.
type Provider struct {
	extraFile string
}

var (
	_ port.NetworkProvider   = Provider{}
	_ port.LocalAddrProvider = Provider{}
)

const extraIfaceFile = "net-ifaces.json"

func New(dataDir string) Provider {
	extra := ""
	if dataDir != "" {
		extra = filepath.Join(dataDir, extraIfaceFile)
	}
	return Provider{extraFile: extra}
}

type ifaceAddr struct {
	name     string
	ip       net.IP
	prefix   int
	loopback bool
}

func (p Provider) Interfaces(_ context.Context) ([]domain.NetInterface, error) {
	raw, err := p.listIPv4()
	if err != nil {
		return nil, fmt.Errorf("list network interfaces: %w", err)
	}
	var out []domain.NetInterface
	for _, a := range raw {
		ip := a.ip.To4()
		if ip == nil || ip.IsLinkLocalUnicast() || ip.IsLoopback() {
			continue
		}
		ipStr := ip.String()
		label := hotspotLabel(a.name, ipStr)
		kind := classify(a.name)
		if label != "" {
			kind = domain.NetPhysical
		}
		out = append(out, domain.NetInterface{
			Name:      a.name,
			IP:        ipStr,
			Kind:      kind,
			PrefixLen: a.prefix,
			Label:     label,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// LocalAddrs returns every IPv4 and IPv6 address of every interface, including
// loopback and interfaces that are down. IPv4-mapped IPv6 addresses are
// returned in their IPv4 form and IPv6 zones are not included.
func (p Provider) LocalAddrs(_ context.Context) ([]netip.Addr, error) {
	raw, err := p.listAddrs()
	if err != nil {
		return nil, fmt.Errorf("list network interfaces: %w", err)
	}
	return raw, nil
}

func addrsFromStdNet() ([]ifaceAddr, []netip.Addr, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, nil, err
	}
	var v4 []ifaceAddr
	var all []netip.Addr
	for _, ifc := range ifaces {
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		up := ifc.Flags&net.FlagUp != 0 && ifc.Flags&net.FlagLoopback == 0
		for _, a := range addrs {
			var raw net.IP
			var prefix int
			switch v := a.(type) {
			case *net.IPNet:
				raw = v.IP
				prefix, _ = v.Mask.Size()
			case *net.IPAddr:
				raw = v.IP
			default:
				continue
			}
			addr, ok := netip.AddrFromSlice(raw)
			if !ok {
				continue
			}
			all = append(all, addr.Unmap())
			if up {
				if ip := raw.To4(); ip != nil {
					v4 = append(v4, ifaceAddr{name: ifc.Name, ip: ip, prefix: prefix})
				}
			}
		}
	}
	return v4, all, nil
}

// parseIPAddrOutput reads `ip -o addr show` lines.
func loadExtraIfaces(path string) []ifaceAddr {
	if path == "" {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return parseIPAddrJSON(raw)
}

func parseIPAddrJSON(raw []byte) []ifaceAddr {
	var rows []struct {
		Name     string `json:"name"`
		IP       string `json:"ip"`
		Prefix   int    `json:"prefix"`
		Loopback bool   `json:"loopback"`
	}
	if json.Unmarshal(raw, &rows) != nil {
		return nil
	}
	out := make([]ifaceAddr, 0, len(rows))
	for _, r := range rows {
		ip := net.ParseIP(r.IP)
		if ip == nil || r.Name == "" {
			continue
		}
		out = append(out, ifaceAddr{name: r.Name, ip: ip, prefix: r.Prefix, loopback: r.Loopback})
	}
	return out
}

func parseIPAddrOutput(out []byte) []ifaceAddr {
	var list []ifaceAddr
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		name := strings.TrimSuffix(fields[1], ":")
		fam := fields[2]
		cidr := fields[3]
		ip, ipnet, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}
		prefix, _ := ipnet.Mask.Size()
		if fam != "inet" && fam != "inet6" {
			continue
		}
		list = append(list, ifaceAddr{name: name, ip: ip, prefix: prefix})
	}
	return list
}

func classify(name string) domain.NetKind {
	lower := strings.ToLower(name)
	for _, p := range []string{"utun", "tun", "tap", "ppp", "ipsec", "wg", "rmnet", "ccmni"} {
		if strings.HasPrefix(lower, p) {
			return domain.NetVPN
		}
	}
	for _, p := range []string{"bridge", "docker", "veth", "vmnet", "vboxnet", "awdl", "llw", "anpi", "gif", "stf", "br-", "virbr", "vethernet", "hyper-v", "vmware", "virtualbox", "dummy"} {
		if strings.HasPrefix(lower, p) {
			return domain.NetVirtual
		}
	}
	return domain.NetPhysical
}

// hotspotLabel names the phone's own Wi-Fi hotspot in the Owner NIC picker.
func hotspotLabel(name, ip string) string {
	if hotspotName(name) || hotspotGateway(ip) {
		return "Hotspot"
	}
	return ""
}

func hotspotName(name string) bool {
	lower := strings.ToLower(name)
	if strings.HasPrefix(lower, "softap") || strings.HasPrefix(lower, "swlan") {
		return true
	}
	if !strings.HasPrefix(lower, "ap") {
		return false
	}
	rest := strings.TrimPrefix(lower, "ap")
	if rest == "" {
		return false
	}
	for _, c := range rest {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func hotspotGateway(ip string) bool {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	v4 := parsed.To4()
	if v4 == nil {
		return false
	}
	// Classic Android hotspot is 192.168.43.1; Android 11+ often uses 192.168.49.1.
	// iPhone Personal Hotspot (this phone is the host) is 172.20.10.1.
	if v4[0] == 172 && v4[1] == 20 && v4[2] == 10 && v4[3] == 1 {
		return true
	}
	return v4[0] == 192 && v4[1] == 168 && (v4[2] == 43 || v4[2] == 49) && v4[3] == 1
}
