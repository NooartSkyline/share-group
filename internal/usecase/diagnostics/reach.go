package diagnostics

import (
	"net/netip"
	"sort"

	"lanshare/internal/domain"
	"lanshare/internal/usecase/port"
)

// ReachVerdict is the outcome of ClassifyReach.
type ReachVerdict string

const (
	// ReachNone: no request from another device was seen in the window.
	// It is indirect evidence only and must never be reported as a confirmed
	// AP Isolation.
	ReachNone ReachVerdict = "none"
	// ReachOutOfSubnet: at least one IPv4 client is outside the subnet of the
	// active interface (guest network or another VLAN).
	ReachOutOfSubnet ReachVerdict = "out_of_subnet"
	// ReachOK: other devices reached the server from the active subnet.
	ReachOK ReachVerdict = "ok"
)

// Reach is the classified view of recent client addresses.
type Reach struct {
	Verdict ReachVerdict
	// Latest is the most recent counted peer (zero value for ReachNone).
	Latest port.PeerSeen
	// OutOfSubnet lists every counted IPv4 peer outside the active subnet,
	// most recent first. Non-empty only for ReachOutOfSubnet.
	OutOfSubnet []port.PeerSeen
}

// ClassifyReach decides what the recent peers say about reachability.
//
// peers must already be limited to the 10 minute window (PeerLog.Since).
// Loopback addresses (127.0.0.0/8, ::1), invalid addresses and addresses in
// locals (assigned to this Mac) are ignored, because they are not other
// devices. IPv4-mapped IPv6 addresses are treated as IPv4.
//
// The subnet is taken from active.IP and active.PrefixLen. If either is
// unusable (PrefixLen 0 means unknown) no peer is called out-of-subnet,
// since that would be a guess. IPv6 peers count as reached but are never
// out-of-subnet. The result does not depend on the order of peers.
func ClassifyReach(peers []port.PeerSeen, locals []netip.Addr, active domain.NetInterface) Reach {
	own := make(map[netip.Addr]struct{}, len(locals))
	for _, a := range locals {
		own[normalizeAddr(a)] = struct{}{}
	}
	subnet, haveSubnet := activeSubnet(active)

	var counted, outside []port.PeerSeen
	for _, p := range peers {
		addr := normalizeAddr(p.Addr)
		if !addr.IsValid() || addr.IsLoopback() {
			continue
		}
		if _, mine := own[addr]; mine {
			continue
		}
		p.Addr = addr
		counted = append(counted, p)
		if haveSubnet && addr.Is4() && !subnet.Contains(addr) {
			outside = append(outside, p)
		}
	}
	if len(counted) == 0 {
		return Reach{Verdict: ReachNone}
	}
	sortRecentFirst(counted)
	r := Reach{Verdict: ReachOK, Latest: counted[0]}
	if len(outside) > 0 {
		sortRecentFirst(outside)
		r.Verdict = ReachOutOfSubnet
		r.OutOfSubnet = outside
	}
	return r
}

// normalizeAddr maps IPv4-mapped IPv6 to IPv4 and drops any zone so the same
// host compares equal however it was written.
func normalizeAddr(a netip.Addr) netip.Addr {
	if !a.IsValid() {
		return a
	}
	return a.Unmap().WithZone("")
}

// activeSubnet returns the IPv4 subnet of the interface, if it is known.
func activeSubnet(active domain.NetInterface) (netip.Prefix, bool) {
	if active.PrefixLen <= 0 || active.PrefixLen > 32 {
		return netip.Prefix{}, false
	}
	ip, err := netip.ParseAddr(active.IP)
	if err != nil {
		return netip.Prefix{}, false
	}
	ip = normalizeAddr(ip)
	if !ip.Is4() {
		return netip.Prefix{}, false
	}
	p, err := ip.Prefix(active.PrefixLen)
	if err != nil {
		return netip.Prefix{}, false
	}
	return p, true
}

// sortRecentFirst orders by LastSeen descending, then address ascending, so
// ties are deterministic.
func sortRecentFirst(ps []port.PeerSeen) {
	sort.SliceStable(ps, func(i, j int) bool {
		if !ps[i].LastSeen.Equal(ps[j].LastSeen) {
			return ps[i].LastSeen.After(ps[j].LastSeen)
		}
		return ps[i].Addr.Less(ps[j].Addr)
	})
}
