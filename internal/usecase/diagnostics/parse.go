// Package diagnostics gathers the environment checks shown to the Owner.
//
// This file holds the pure parsers for the output of macOS commands. They do
// no I/O and never guess: any text that is not recognised exactly yields
// ErrParseFailed, so the caller reports the check as "unknown" with reason
// ReasonParseFailed instead of a wrong "ok". Errors never embed the raw
// output, because that output may contain a Wi-Fi name or a file path.
package diagnostics

import (
	"bytes"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
)

// Stable English codes for CheckResult.Reason.
const (
	ReasonCommandMissing = "command_missing"
	ReasonCommandFailed  = "command_failed"
	ReasonParseFailed    = "parse_failed"
	ReasonTimeout        = "timeout"
	ReasonNotWifi        = "not_wifi"
	ReasonPermissionDeny = "permission_denied"
	ReasonNoNetwork      = "no_network"
)

// maxFirewallOutputSize bounds the one-line firewall outputs we accept.
const maxFirewallOutputSize = 4096

var (
	// ErrParseFailed means the text was not in a format this package knows.
	ErrParseFailed = errors.New("diagnostics: output not understood")
	// ErrNoNetwork means the Wi-Fi interface exists but is not joined to a
	// network, so it has no channel (reason ReasonNoNetwork).
	ErrNoNetwork = errors.New("diagnostics: wi-fi interface not connected")
)

// FirewallGlobal is the result of `socketfilterfw --getglobalstate`.
type FirewallGlobal struct {
	// Enabled is false only for State 0.
	Enabled bool
	// State is the number printed by the tool: 0 off, 1 on, 2 on with
	// additional restrictions.
	State int
}

var globalStateRe = regexp.MustCompile(`^Firewall is ([^(]*?)\.? \(State = ([0-9]+)\)$`)

// ParseFirewallGlobal parses `socketfilterfw --getglobalstate`.
//
// Only the verified text "Firewall is disabled. (State = 0)" and the same
// shape for states 1 and 2 are accepted. A state number that contradicts its
// wording, or any other state number, is ErrParseFailed.
func ParseFirewallGlobal(out []byte) (FirewallGlobal, error) {
	line, ok := singleLine(out)
	if !ok {
		return FirewallGlobal{}, ErrParseFailed
	}
	m := globalStateRe.FindStringSubmatch(line)
	if m == nil {
		return FirewallGlobal{}, ErrParseFailed
	}
	state, err := strconv.Atoi(m[2])
	if err != nil {
		return FirewallGlobal{}, ErrParseFailed
	}
	disabled := m[1] == "disabled"
	switch state {
	case 0:
		if !disabled {
			return FirewallGlobal{}, ErrParseFailed
		}
		return FirewallGlobal{Enabled: false, State: 0}, nil
	case 1, 2:
		if disabled || m[1] == "" {
			return FirewallGlobal{}, ErrParseFailed
		}
		return FirewallGlobal{Enabled: true, State: state}, nil
	default:
		return FirewallGlobal{}, ErrParseFailed
	}
}

// ParseFirewallBlockAll parses `socketfilterfw --getblockall` and reports
// whether "Block all incoming connections" is on.
func ParseFirewallBlockAll(out []byte) (bool, error) {
	line, ok := singleLine(out)
	if !ok {
		return false, ErrParseFailed
	}
	switch line {
	case "Firewall has block all state set to disabled.":
		return false, nil
	case "Firewall has block all state set to enabled.":
		return true, nil
	}
	return false, ErrParseFailed
}

// AppAccess says whether the firewall lets a program accept connections.
type AppAccess string

const (
	AppPermitted AppAccess = "permitted"
	AppBlocked   AppAccess = "blocked"
)

const (
	appBlockedPrefix    = "Incoming connection to "
	appPermittedSuffix  = " is permitted."
	appBlockedSuffixTxt = " is blocked."
)

// ParseFirewallAppBlocked parses `socketfilterfw --getappblocked <path>`.
//
// Any other wording, including whatever the tool prints for a program that is
// not in its list, is ErrParseFailed. The path is not returned.
func ParseFirewallAppBlocked(out []byte) (AppAccess, error) {
	line, ok := singleLine(out)
	if !ok || !strings.HasPrefix(line, appBlockedPrefix) {
		return "", ErrParseFailed
	}
	rest := strings.TrimPrefix(line, appBlockedPrefix)
	switch {
	case strings.HasSuffix(rest, appPermittedSuffix) && len(rest) > len(appPermittedSuffix):
		return AppPermitted, nil
	case strings.HasSuffix(rest, appBlockedSuffixTxt) && len(rest) > len(appBlockedSuffixTxt):
		return AppBlocked, nil
	}
	return "", ErrParseFailed
}

// HardwarePort is one entry of `networksetup -listallhardwareports`.
type HardwarePort struct {
	Name   string // e.g. "Wi-Fi", "Thunderbolt Bridge"
	Device string // e.g. "en0"
}

// ParseHardwarePorts parses `networksetup -listallhardwareports`. Entries
// without a Device line are dropped. Output with no "Hardware Port:" line at
// all is ErrParseFailed.
func ParseHardwarePorts(out []byte) ([]HardwarePort, error) {
	const (
		portKey   = "Hardware Port:"
		deviceKey = "Device:"
		vlanKey   = "VLAN Configurations"
	)
	var (
		ports  []HardwarePort
		cur    *HardwarePort
		sawKey bool
	)
	flush := func() {
		if cur != nil && cur.Device != "" {
			ports = append(ports, *cur)
		}
		cur = nil
	}
	for _, raw := range strings.Split(string(out), "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(line, vlanKey):
			flush()
			if !sawKey {
				return nil, ErrParseFailed
			}
			return ports, nil
		case strings.HasPrefix(line, portKey):
			flush()
			sawKey = true
			cur = &HardwarePort{Name: strings.TrimSpace(strings.TrimPrefix(line, portKey))}
		case strings.HasPrefix(line, deviceKey):
			if cur != nil {
				cur.Device = strings.TrimSpace(strings.TrimPrefix(line, deviceKey))
			}
		}
	}
	flush()
	if !sawKey {
		return nil, ErrParseFailed
	}
	return ports, nil
}

// IsWiFiDevice reports whether device (e.g. "en0") is listed under a Wi-Fi
// hardware port. The port name is "Wi-Fi" on current macOS and "AirPort" on
// old releases.
func IsWiFiDevice(ports []HardwarePort, device string) bool {
	if device == "" {
		return false
	}
	for _, p := range ports {
		if p.Device != device {
			continue
		}
		if strings.EqualFold(p.Name, "Wi-Fi") || strings.EqualFold(p.Name, "AirPort") {
			return true
		}
	}
	return false
}

// WifiBand is the radio band a Wi-Fi channel belongs to.
type WifiBand string

const (
	// Band2GHz is the 2.4 GHz band, which macOS prints as "2GHz".
	Band2GHz WifiBand = "2GHz"
	Band5GHz WifiBand = "5GHz"
	Band6GHz WifiBand = "6GHz"
)

// WifiChannel is a parsed channel string such as "149 (5GHz, 20MHz)".
type WifiChannel struct {
	Number   int
	Band     WifiBand
	WidthMHz int
}

var channelRe = regexp.MustCompile(`^([0-9]{1,4}) \(([0-9]{1,2})GHz, ([0-9]{1,4})MHz\)$`)

// ParseWifiChannel splits "<n> (<band>GHz, <width>MHz)". Only bands 2, 5 and 6
// are accepted; anything else is ErrParseFailed.
func ParseWifiChannel(s string) (WifiChannel, error) {
	m := channelRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return WifiChannel{}, ErrParseFailed
	}
	n, err1 := strconv.Atoi(m[1])
	width, err2 := strconv.Atoi(m[3])
	if err1 != nil || err2 != nil {
		return WifiChannel{}, ErrParseFailed
	}
	var band WifiBand
	switch m[2] {
	case "2":
		band = Band2GHz
	case "5":
		band = Band5GHz
	case "6":
		band = Band6GHz
	default:
		return WifiChannel{}, ErrParseFailed
	}
	return WifiChannel{Number: n, Band: band, WidthMHz: width}, nil
}

// ParseWifiChannelFor reads the channel of the interface named iface from the
// JSON of `system_profiler SPAirPortDataType -json`.
//
// Only spairport_current_network_information.spairport_network_channel of the
// interface whose _name equals iface is read. Nothing else is decoded, so the
// neighbouring networks listed under "Other Local Wi-Fi Networks" can never
// leak into the result. An interface that exists but has no current network
// is ErrNoNetwork; an unknown interface or malformed JSON is ErrParseFailed.
func ParseWifiChannelFor(jsonOut []byte, iface string) (WifiChannel, error) {
	if iface == "" {
		return WifiChannel{}, ErrParseFailed
	}
	var doc struct {
		Cards []struct {
			Interfaces []json.RawMessage `json:"spairport_airport_interfaces"`
		} `json:"SPAirPortDataType"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(jsonOut), &doc); err != nil {
		return WifiChannel{}, ErrParseFailed
	}
	for _, card := range doc.Cards {
		for _, raw := range card.Interfaces {
			var name struct {
				Name string `json:"_name"`
			}
			if json.Unmarshal(raw, &name) != nil || name.Name != iface {
				continue
			}
			var cur struct {
				Current *struct {
					Channel string `json:"spairport_network_channel"`
				} `json:"spairport_current_network_information"`
			}
			if json.Unmarshal(raw, &cur) != nil {
				return WifiChannel{}, ErrParseFailed
			}
			if cur.Current == nil || cur.Current.Channel == "" {
				return WifiChannel{}, ErrNoNetwork
			}
			return ParseWifiChannel(cur.Current.Channel)
		}
	}
	return WifiChannel{}, ErrParseFailed
}

// singleLine returns the only non-empty line of out, trimmed. Output that is
// empty, has several non-empty lines, or is implausibly large is rejected.
func singleLine(out []byte) (string, bool) {
	if len(out) == 0 || len(out) > maxFirewallOutputSize {
		return "", false
	}
	var line string
	for _, l := range strings.Split(string(out), "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if line != "" {
			return "", false
		}
		line = l
	}
	return line, line != ""
}
