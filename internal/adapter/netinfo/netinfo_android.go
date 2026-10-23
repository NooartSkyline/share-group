//go:build android

package netinfo

import (
	"net/netip"
	"os"
	"os/exec"
)

func ipBin() string {
	for _, p := range []string{"/system/bin/ip", "/system/xbin/ip"} {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return "ip"
}

func (p Provider) listIPv4() ([]ifaceAddr, error) {
	if extra := loadExtraIfaces(p.extraFile); len(extra) > 0 {
		return extra, nil
	}
	if v4, _, err := addrsFromStdNet(); err == nil && len(v4) > 0 {
		return v4, nil
	}
	out, err := exec.Command(ipBin(), "-4", "-o", "addr", "show").Output()
	if err != nil {
		return nil, err
	}
	return parseIPAddrOutput(out), nil
}

func (p Provider) listAddrs() ([]netip.Addr, error) {
	if extra := loadExtraIfaces(p.extraFile); len(extra) > 0 {
		var all []netip.Addr
		for _, a := range extra {
			addr, ok := netip.AddrFromSlice(a.ip)
			if !ok {
				continue
			}
			all = append(all, addr.Unmap())
		}
		return all, nil
	}
	if _, all, err := addrsFromStdNet(); err == nil && len(all) > 0 {
		return all, nil
	}
	out, err := exec.Command(ipBin(), "-o", "addr", "show").Output()
	if err != nil {
		return nil, err
	}
	var all []netip.Addr
	for _, a := range parseIPAddrOutput(out) {
		addr, ok := netip.AddrFromSlice(a.ip)
		if !ok {
			continue
		}
		all = append(all, addr.Unmap())
	}
	return all, nil
}
