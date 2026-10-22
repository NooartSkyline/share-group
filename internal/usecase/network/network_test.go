package network

import (
	"context"
	"strings"
	"testing"

	"lanshare/internal/domain"
	"lanshare/internal/usecase/fake"
)

type netList []domain.NetInterface

func (n netList) Interfaces(context.Context) ([]domain.NetInterface, error) { return n, nil }

func owner() domain.Device {
	return domain.Device{Role: domain.RoleOwner, RoomID: "main"}
}

func TestPickAutoSkipsUSBWhenWiFiExists(t *testing.T) {
	svc := NewService(netList{
		{Name: "en5", IP: "172.20.10.2", Kind: domain.NetPhysical},
		{Name: "en0", IP: "192.168.1.23", Kind: domain.NetPhysical},
	}, 8080, "")
	opts, err := svc.List(context.Background(), owner())
	if err != nil {
		t.Fatal(err)
	}
	var usb, wifi, recUSB, recWiFi bool
	for _, o := range opts {
		if o.Name == "en5" {
			usb = o.Kind == domain.NetUSB
			recUSB = o.Recommended
		}
		if o.Name == "en0" {
			wifi = o.Kind == domain.NetPhysical && o.Selected && o.Recommended
			recWiFi = o.Recommended
		}
	}
	if !usb {
		t.Fatalf("en5 should be classified USB: %+v", opts)
	}
	if !wifi || recUSB || !recWiFi {
		t.Fatalf("Wi-Fi should stay the advertised interface: %+v", opts)
	}
}

func TestUSBEthernetOnLANIsPhysical(t *testing.T) {
	svc := NewService(netList{
		{Name: "en0", IP: "192.168.1.23", Kind: domain.NetPhysical},
		{Name: "en7", IP: "192.168.1.50", Kind: domain.NetPhysical},
	}, 8080, "")
	run := fake.NewRunner()
	run.On("/usr/sbin/networksetup -listallhardwareports", fake.RunResult{Out: []byte("" +
		"Hardware Port: USB 10/100/1000 LAN\nDevice: en7\nEthernet Address: 00:00:00:00:00:00\n\n" +
		"Hardware Port: Wi-Fi\nDevice: en0\nEthernet Address: 00:00:00:00:00:00\n")})
	svc.SetRunner(run)
	opts, err := svc.List(context.Background(), owner())
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range opts {
		if o.Name == "en7" && o.Kind != domain.NetPhysical {
			t.Fatalf("USB Ethernet on the LAN should stay physical: %+v", o)
		}
	}
	st, err := svc.Cable(context.Background(), owner())
	if err != nil {
		t.Fatal(err)
	}
	if st.State != CableIdle || len(st.Links) != 0 {
		t.Fatalf("USB Ethernet on the LAN is not a phone cable: %+v", st)
	}
}

func TestUSBIsLastResortWhenAlone(t *testing.T) {
	svc := NewService(netList{
		{Name: "en5", IP: "172.20.10.2", Kind: domain.NetPhysical},
	}, 8080, "")
	n, err := svc.Active(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n.Kind != domain.NetUSB || n.IP != "172.20.10.2" {
		t.Fatalf("active = %+v", n)
	}
}

func TestSelectUSBInterface(t *testing.T) {
	svc := NewService(netList{
		{Name: "en5", IP: "172.20.10.2", Kind: domain.NetPhysical},
		{Name: "en0", IP: "192.168.1.23", Kind: domain.NetPhysical},
	}, 8080, "")
	if err := svc.Select(context.Background(), owner(), "en5"); err != nil {
		t.Fatal(err)
	}
	n, err := svc.Active(context.Background())
	if err != nil || n.Name != "en5" || n.Kind != domain.NetUSB {
		t.Fatalf("active = %+v, %v", n, err)
	}
}

func TestMemberCannotListOrCable(t *testing.T) {
	svc := NewService(netList{{Name: "en0", IP: "192.168.1.23", Kind: domain.NetPhysical}}, 8080, "")
	member := domain.Device{Role: domain.RoleMember, RoomID: "main"}
	if _, err := svc.List(context.Background(), member); err != domain.ErrForbidden {
		t.Errorf("List = %v", err)
	}
	if _, err := svc.Cable(context.Background(), member); err != domain.ErrForbidden {
		t.Errorf("Cable = %v", err)
	}
}

func TestCableIdleWithoutUSB(t *testing.T) {
	svc := NewService(netList{{Name: "en0", IP: "192.168.1.23", Kind: domain.NetPhysical}}, 8080, "")
	st, err := svc.Cable(context.Background(), owner())
	if err != nil {
		t.Fatal(err)
	}
	if st.State != CableIdle || len(st.Links) != 0 {
		t.Fatalf("%+v", st)
	}
}

func TestCableReadyFromAndroidTetherIP(t *testing.T) {
	svc := NewService(netList{
		{Name: "en0", IP: "192.168.1.23", Kind: domain.NetPhysical},
		{Name: "en8", IP: "192.168.42.184", Kind: domain.NetPhysical, PrefixLen: 24},
	}, 8080, "")
	st, err := svc.Cable(context.Background(), owner())
	if err != nil {
		t.Fatal(err)
	}
	if st.State != CableReady || len(st.Links) != 1 {
		t.Fatalf("%+v", st)
	}
	link := st.Links[0]
	if link.Device != domain.CableAndroid || link.IP != "192.168.42.184" {
		t.Fatalf("link = %+v", link)
	}
	if link.URL != "http://192.168.42.184:8080/?r=main" {
		t.Errorf("url = %s", link.URL)
	}
}

func TestCableReadyFromHardwarePortName(t *testing.T) {
	run := fake.NewRunner()
	run.On("/usr/sbin/networksetup -listallhardwareports", fake.RunResult{Out: []byte("" +
		"Hardware Port: iPhone USB\nDevice: en5\nEthernet Address: 00:00:00:00:00:00\n\n" +
		"Hardware Port: Wi-Fi\nDevice: en0\nEthernet Address: 00:00:00:00:00:00\n")})
	run.On("/usr/sbin/ioreg -r -c IOUSBHostDevice -l -d 1 -w 0", fake.RunResult{Out: []byte(ioregIPhone)})
	svc := NewService(netList{
		{Name: "en0", IP: "192.168.1.23", Kind: domain.NetPhysical},
		{Name: "en5", IP: "10.0.0.2", Kind: domain.NetPhysical}, // not a known USB range
	}, 8080, "")
	svc.SetRunner(run)
	st, err := svc.Cable(context.Background(), owner())
	if err != nil {
		t.Fatal(err)
	}
	if st.State != CableReady || len(st.Links) != 1 || st.Links[0].Device != domain.CableiPhone {
		t.Fatalf("%+v", st)
	}
	if st.Links[0].Label != "iPhone USB" {
		t.Errorf("label = %q", st.Links[0].Label)
	}
	if len(st.Phones) != 1 || st.Phones[0].Device != domain.CableiPhone {
		t.Errorf("phones = %+v", st.Phones)
	}
}

func TestCablePluggedWithoutNetwork(t *testing.T) {
	run := fake.NewRunner()
	run.On("/usr/sbin/networksetup -listallhardwareports", fake.RunResult{Out: []byte("Hardware Port: Wi-Fi\nDevice: en0\n")})
	run.On("/usr/sbin/ioreg -r -c IOUSBHostDevice -l -d 1 -w 0", fake.RunResult{Out: []byte(ioregPixel)})
	svc := NewService(netList{{Name: "en0", IP: "192.168.1.23", Kind: domain.NetPhysical}}, 8080, "")
	svc.SetRunner(run)
	st, err := svc.Cable(context.Background(), owner())
	if err != nil {
		t.Fatal(err)
	}
	if st.State != CablePlugged || len(st.Links) != 0 {
		t.Fatalf("%+v", st)
	}
	if len(st.Phones) != 1 || st.Phones[0].Device != domain.CableAndroid || st.Phones[0].Name != "Pixel 8" {
		t.Errorf("phones = %+v", st.Phones)
	}
}

func TestClassifyUSBIdentity(t *testing.T) {
	cases := []struct {
		hw, ip, device string
	}{
		{"iPhone USB", "169.254.1.1", domain.CableiPhone},
		{"iPad USB", "10.0.0.2", domain.CableiPad},
		{"USB 10/100/1000 LAN", "192.168.42.10", domain.CableAndroid},
		{"USB 10/100/1000 LAN", "192.168.1.50", ""},
		{"RNDIS/Ethernet Gadget", "10.1.1.1", domain.CableAndroid},
		{"Wi-Fi", "192.168.1.23", ""},
		{"Ethernet Adapter (en4)", "192.168.1.50", ""},
		{"", "172.20.10.2", domain.CableiPhone},
		{"", "192.168.42.184", domain.CableAndroid},
		{"", "192.168.1.23", ""},
	}
	for _, c := range cases {
		d, _ := usbIdentity(c.hw, c.ip)
		if d != c.device {
			t.Errorf("usbIdentity(%q, %q) = %q, want %q", c.hw, c.ip, d, c.device)
		}
	}
}

func TestParseUSBPhonesIgnoresHubs(t *testing.T) {
	phones := parseUSBPhones([]byte(ioregIPhone + ioregBillboard))
	if len(phones) != 1 || phones[0].Device != domain.CableiPhone {
		t.Fatalf("%+v", phones)
	}
}

func TestViaUSB(t *testing.T) {
	if !ViaUSB("172.20.10.2") || !ViaUSB("192.168.42.184") {
		t.Fatal("tether addresses should be USB")
	}
	if ViaUSB("192.168.1.23") || ViaUSB("10.0.0.8") || ViaUSB("") {
		t.Fatal("LAN addresses should not be USB")
	}
}

func TestHTTPSCableURL(t *testing.T) {
	svc := NewService(netList{{Name: "en5", IP: "172.20.10.2", Kind: domain.NetPhysical}}, 8080, "")
	svc.SetSecure(true)
	st, err := svc.Cable(context.Background(), owner())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(st.Links[0].URL, "https://172.20.10.2:8080/") {
		t.Errorf("url = %s", st.Links[0].URL)
	}
}

const ioregIPhone = `
+-o iPhone@00100000  <class IOUSBHostDevice, id 0x1, registered, matched, active, busy 0 (0 ms), retain 20>
    {
      "USB Product Name" = "iPhone"
      "USB Vendor Name" = "Apple Inc."
      "idVendor" = 1452
    }
`

const ioregPixel = `
+-o Pixel 8@00200000  <class IOUSBHostDevice, id 0x2, registered, matched, active, busy 0 (0 ms), retain 18>
    {
      "USB Product Name" = "Pixel 8"
      "USB Vendor Name" = "Google"
    }
`

const ioregBillboard = `
+-o USB 2.0 BILLBOARD@03100000  <class IOUSBHostDevice, id 0x3, registered, matched, active, busy 0 (0 ms), retain 10>
    {
      "USB Product Name" = "USB 2.0 BILLBOARD"
      "USB Vendor Name" = "VIA Technologies Inc."
    }
`
