package netinfo

import (
	"testing"
)

func TestHotspotLabel(t *testing.T) {
	cases := []struct {
		name, ip, want string
	}{
		{"ap0", "192.168.43.1", "Hotspot"},
		{"softap0", "10.0.0.1", "Hotspot"},
		{"swlan0", "192.168.1.1", "Hotspot"},
		{"wlan0", "192.168.49.1", "Hotspot"},
		{"bridge100", "172.20.10.1", "Hotspot"},
		{"wlan0", "192.168.43.50", ""},
		{"en0", "192.168.1.23", ""},
		{"apple0", "10.0.0.1", ""},
	}
	for _, c := range cases {
		if got := hotspotLabel(c.name, c.ip); got != c.want {
			t.Errorf("hotspotLabel(%q, %q) = %q, want %q", c.name, c.ip, got, c.want)
		}
	}
}

func TestClassifyLeavesHotspotPhysical(t *testing.T) {
	if got := classify("ap0"); got != "physical" {
		t.Errorf("classify(ap0) = %q, want physical", got)
	}
}

func TestClassifyBridgeWithoutHotspotIPIsVirtual(t *testing.T) {
	if got := classify("bridge100"); got != "virtual" {
		t.Errorf("classify(bridge100) = %q, want virtual", got)
	}
}

func TestParseIPAddrJSON(t *testing.T) {
	got := parseIPAddrJSON([]byte(`[{"name":"wlan0","ip":"10.88.120.245","prefix":23}]`))
	if len(got) != 1 || got[0].name != "wlan0" || got[0].ip.String() != "10.88.120.245" || got[0].prefix != 23 {
		t.Fatalf("%+v", got)
	}
}

func TestParseIPAddrOutput(t *testing.T) {
	raw := []byte("1: lo    inet 127.0.0.1/8 scope host lo\n45: wlan0    inet 10.88.120.245/23 brd 10.88.121.255 scope global dynamic wlan0\n")
	got := parseIPAddrOutput(raw)
	if len(got) != 2 || got[1].name != "wlan0" || got[1].ip.String() != "10.88.120.245" || got[1].prefix != 23 {
		t.Fatalf("%+v", got)
	}
}
