//go:build !android

package netinfo

import "net/netip"

func (Provider) listIPv4() ([]ifaceAddr, error) {
	v4, _, err := addrsFromStdNet()
	return v4, err
}

func (Provider) listAddrs() ([]netip.Addr, error) {
	_, all, err := addrsFromStdNet()
	return all, err
}
