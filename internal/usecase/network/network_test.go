package network

import (
	"context"
	"strings"
	"testing"
	"time"

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

func TestHostPersonalHotspotIsNotUSB(t *testing.T) {
	svc := NewService(netList{
		{Name: "bridge100", IP: "172.20.10.1", Kind: domain.NetPhysical, Label: "Hotspot"},
		{Name: "en0", IP: "192.168.1.23", Kind: domain.NetPhysical},
	}, 8080, "")
	opts, err := svc.List(context.Background(), owner())
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range opts {
		if o.Name == "bridge100" && o.Kind == domain.NetUSB {
			t.Fatalf("host Personal Hotspot must stay pickable, not USB: %+v", o)
		}
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

func TestCableReadyFromADBReverse(t *testing.T) {
	run := fake.NewRunner()
	run.On("/usr/sbin/networksetup -listallhardwareports", fake.RunResult{Out: []byte("Hardware Port: Wi-Fi\nDevice: en0\n")})
	run.On("/usr/sbin/ioreg -r -c IOUSBHostDevice -l -d 1 -w 0", fake.RunResult{Out: []byte(ioregPixel)})
	run.On("adb version", fake.RunResult{Out: []byte("Android Debug Bridge version 1.0.41")})
	run.On("adb devices", fake.RunResult{Out: []byte("List of devices attached\nR58M123\tdevice\n")})
	run.On("adb reverse tcp:8080 tcp:8080", fake.RunResult{})
	run.On("adb shell uname -r", fake.RunResult{Out: []byte("5.15.189-android13-8\n")})
	run.On("adb shell getprop ro.build.version.release", fake.RunResult{Out: []byte("16\n")})
	svc := NewService(netList{{Name: "en0", IP: "192.168.1.23", Kind: domain.NetPhysical}}, 8080, "")
	svc.SetRunner(run)
	st, err := svc.Cable(context.Background(), owner())
	if err != nil {
		t.Fatal(err)
	}
	if st.State != CableReady || len(st.Links) != 1 {
		t.Fatalf("%+v", st)
	}
	if st.Links[0].IP != "127.0.0.1" || st.Links[0].URL != "http://127.0.0.1:8080/?r=main" {
		t.Fatalf("link = %+v", st.Links[0])
	}
	if st.Links[0].Device != domain.CableAndroid || st.Links[0].Label != "Pixel 8" {
		t.Errorf("device = %+v", st.Links[0])
	}
	if len(st.Phones) != 1 || st.Phones[0].Kernel != "5.15.189-android13-8" || st.Phones[0].Android != "16" {
		t.Errorf("phones = %+v", st.Phones)
	}
	if st.Phones[0].Path != domain.CableAndroid || st.Phones[0].Serial != "R58M123" {
		t.Errorf("path = %+v", st.Phones[0])
	}
}

func TestCableTwoAndroidPhonesHaveOwnPaths(t *testing.T) {
	run := fake.NewRunner()
	run.On("/usr/sbin/networksetup -listallhardwareports", fake.RunResult{Out: []byte("Hardware Port: Wi-Fi\nDevice: en0\n")})
	run.On("/usr/sbin/ioreg -r -c IOUSBHostDevice -l -d 1 -w 0", fake.RunResult{Out: []byte(ioregPixel)})
	run.On("adb version", fake.RunResult{Out: []byte("Android Debug Bridge version 1.0.41")})
	run.On("adb devices", fake.RunResult{Out: []byte("List of devices attached\nRFCWA19V99W\tdevice\nc3782355\tdevice\n")})
	run.On("adb -s RFCWA19V99W reverse tcp:8080 tcp:8080", fake.RunResult{})
	run.On("adb -s c3782355 reverse tcp:8080 tcp:8080", fake.RunResult{})
	run.On("adb -s RFCWA19V99W shell getprop ro.product.model", fake.RunResult{Out: []byte("SM-S911B\n")})
	run.On("adb -s RFCWA19V99W shell getprop ro.build.version.release", fake.RunResult{Out: []byte("16\n")})
	run.On("adb -s RFCWA19V99W shell uname -r", fake.RunResult{Out: []byte("5.15.189-samsung\n")})
	run.On("adb -s c3782355 shell getprop ro.product.model", fake.RunResult{Out: []byte("CPH1613\n")})
	run.On("adb -s c3782355 shell getprop ro.build.version.release", fake.RunResult{Out: []byte("12\n")})
	run.On("adb -s c3782355 shell uname -r", fake.RunResult{Out: []byte("4.14.oppo\n")})
	svc := NewService(netList{{Name: "en0", IP: "192.168.1.23", Kind: domain.NetPhysical}}, 8080, "")
	svc.SetRunner(run)
	st, err := svc.Cable(context.Background(), owner())
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Phones) != 2 {
		t.Fatalf("phones = %+v", st.Phones)
	}
	if st.Phones[0].Name != "SM-S911B" || st.Phones[0].Path != "android:RFCWA19V99W" || st.Phones[0].Kernel != "5.15.189-samsung" {
		t.Fatalf("first = %+v", st.Phones[0])
	}
	if st.Phones[1].Name != "CPH1613" || st.Phones[1].Path != "android:c3782355" || st.Phones[1].Android != "12" {
		t.Fatalf("second = %+v", st.Phones[1])
	}
}

func TestCableKeepsUSBAndroidWithoutAdb(t *testing.T) {
	run := fake.NewRunner()
	run.On("/usr/sbin/networksetup -listallhardwareports", fake.RunResult{Out: []byte("Hardware Port: Wi-Fi\nDevice: en0\n")})
	run.On("/usr/sbin/ioreg -r -c IOUSBHostDevice -l -d 1 -w 0", fake.RunResult{Out: []byte(ioregPixel + ioregGenericAndroid)})
	run.On("adb version", fake.RunResult{Out: []byte("Android Debug Bridge version 1.0.41")})
	run.On("adb devices", fake.RunResult{Out: []byte("List of devices attached\nR58M123\tdevice\n")})
	run.On("adb reverse tcp:8080 tcp:8080", fake.RunResult{})
	run.On("adb shell uname -r", fake.RunResult{Out: []byte("5.15.189-android13-8\n")})
	run.On("adb shell getprop ro.build.version.release", fake.RunResult{Out: []byte("16\n")})
	svc := NewService(netList{{Name: "en0", IP: "192.168.1.23", Kind: domain.NetPhysical}}, 8080, "")
	svc.SetRunner(run)
	st, err := svc.Cable(context.Background(), owner())
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Phones) != 2 {
		t.Fatalf("phones = %+v", st.Phones)
	}
	if st.Phones[0].Path != domain.CableAndroid || st.Phones[0].Serial != "R58M123" {
		t.Fatalf("adb phone = %+v", st.Phones[0])
	}
	if st.Phones[1].Name != "Android" || st.Phones[1].Path != "" || st.Phones[1].Serial != "" {
		t.Fatalf("extra = %+v", st.Phones[1])
	}
}

func TestKeepCableStopsOnCancel(t *testing.T) {
	run := fake.NewRunner()
	run.On("/usr/sbin/networksetup -listallhardwareports", fake.RunResult{Out: []byte("Hardware Port: Wi-Fi\nDevice: en0\n")})
	run.On("/usr/sbin/ioreg -r -c IOUSBHostDevice -l -d 1 -w 0", fake.RunResult{Out: []byte(ioregPixel)})
	run.On("adb version", fake.RunResult{Out: []byte("Android Debug Bridge version 1.0.41")})
	run.On("adb devices", fake.RunResult{Out: []byte("List of devices attached\nR58M123\tdevice\n")})
	run.On("adb reverse tcp:8080 tcp:8080", fake.RunResult{})
	svc := NewService(netList{{Name: "en0", IP: "192.168.1.23", Kind: domain.NetPhysical}}, 8080, "")
	svc.SetRunner(run)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		svc.KeepCable(ctx, "main")
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("KeepCable did not stop")
	}
}

func TestCableADBUnauthorizedStaysPlugged(t *testing.T) {
	run := fake.NewRunner()
	run.On("/usr/sbin/networksetup -listallhardwareports", fake.RunResult{Out: []byte("Hardware Port: Wi-Fi\nDevice: en0\n")})
	run.On("/usr/sbin/ioreg -r -c IOUSBHostDevice -l -d 1 -w 0", fake.RunResult{Out: []byte(ioregPixel)})
	run.On("adb version", fake.RunResult{Out: []byte("Android Debug Bridge version 1.0.41")})
	run.On("adb devices", fake.RunResult{Out: []byte("List of devices attached\nR58M123\tunauthorized\n")})
	svc := NewService(netList{{Name: "en0", IP: "192.168.1.23", Kind: domain.NetPhysical}}, 8080, "")
	svc.SetRunner(run)
	st, err := svc.Cable(context.Background(), owner())
	if err != nil {
		t.Fatal(err)
	}
	if st.State != CablePlugged || len(st.Links) != 0 {
		t.Fatalf("%+v", st)
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

func TestOpenOnCableForbiddenForMember(t *testing.T) {
	svc := NewService(netList{{Name: "en0", IP: "192.168.1.23", Kind: domain.NetPhysical}}, 8080, "")
	if _, err := svc.OpenOnCable(context.Background(), domain.Device{Role: domain.RoleMember, RoomID: "main"}); err != domain.ErrForbidden {
		t.Errorf("OpenOnCable = %v", err)
	}
}

func TestOpenOnCableIdleNotFound(t *testing.T) {
	svc := NewService(netList{{Name: "en0", IP: "192.168.1.23", Kind: domain.NetPhysical}}, 8080, "")
	if _, err := svc.OpenOnCable(context.Background(), owner()); err != domain.ErrNotFound {
		t.Errorf("OpenOnCable idle = %v", err)
	}
}

func TestOpenOnCableAndroidLaunchesBrowser(t *testing.T) {
	run := fake.NewRunner()
	run.On("/usr/sbin/networksetup -listallhardwareports", fake.RunResult{Out: []byte("Hardware Port: Wi-Fi\nDevice: en0\n")})
	run.On("/usr/sbin/ioreg -r -c IOUSBHostDevice -l -d 1 -w 0", fake.RunResult{Out: []byte(ioregPixel)})
	run.On("adb version", fake.RunResult{Out: []byte("Android Debug Bridge version 1.0.41")})
	run.On("adb devices", fake.RunResult{Out: []byte("List of devices attached\nR58M123\tdevice\n")})
	run.On("adb reverse tcp:8080 tcp:8080", fake.RunResult{})
	run.On("adb shell am start -a android.intent.action.VIEW -d http://127.0.0.1:8080/?r=main", fake.RunResult{})
	svc := NewService(netList{{Name: "en0", IP: "192.168.1.23", Kind: domain.NetPhysical}}, 8080, "")
	svc.SetRunner(run)
	got, err := svc.OpenOnCable(context.Background(), owner())
	if err != nil {
		t.Fatal(err)
	}
	if !got.Opened || got.Device != domain.CableAndroid || got.URL != "http://127.0.0.1:8080/?r=main" {
		t.Fatalf("%+v", got)
	}
	var launched bool
	for _, c := range run.Calls() {
		if len(c.Args) >= 2 && c.Args[0] == "shell" && c.Args[1] == "am" {
			launched = true
		}
	}
	if !launched {
		t.Fatal("expected adb shell am start")
	}
}

func TestOpenOnCableiPhoneReturnsUSBURL(t *testing.T) {
	run := fake.NewRunner()
	run.On("/usr/sbin/networksetup -listallhardwareports", fake.RunResult{Out: []byte(
		"Hardware Port: iPhone USB\nDevice: en5\nEthernet Address: 00:00:00:00:00:00\n")})
	run.On("/usr/sbin/ioreg -r -c IOUSBHostDevice -l -d 1 -w 0", fake.RunResult{Out: []byte(ioregIPhone)})
	svc := NewService(netList{
		{Name: "en0", IP: "192.168.1.23", Kind: domain.NetPhysical},
		{Name: "en5", IP: "172.20.10.2", Kind: domain.NetPhysical},
	}, 8080, "")
	svc.SetRunner(run)
	got, err := svc.OpenOnCable(context.Background(), owner())
	if err != nil {
		t.Fatal(err)
	}
	if got.Opened {
		t.Fatalf("iOS should not auto-open without a device tool: %+v", got)
	}
	if got.Device != domain.CableiPhone || got.URL != "http://172.20.10.2:8080/?r=main" {
		t.Fatalf("%+v", got)
	}
	if got.Hint == "" {
		t.Fatal("want a Safari hint")
	}
}

func TestOpenOnCableiOSSafariWhenDevicectlWorks(t *testing.T) {
	run := fake.NewRunner()
	run.On("/usr/sbin/networksetup -listallhardwareports", fake.RunResult{Out: []byte(
		"Hardware Port: iPhone USB\nDevice: en5\nEthernet Address: 00:00:00:00:00:00\n")})
	run.On("/usr/sbin/ioreg -r -c IOUSBHostDevice -l -d 1 -w 0", fake.RunResult{Out: []byte(ioregIPhone)})
	run.On("idevice_id -l", fake.RunResult{Out: []byte("00008110-001A4D123456789A\n")})
	run.On("xcrun devicectl device process launch --device 00008110-001A4D123456789A com.apple.mobilesafari", fake.RunResult{})
	svc := NewService(netList{
		{Name: "en5", IP: "172.20.10.2", Kind: domain.NetPhysical},
	}, 8080, "")
	svc.SetRunner(run)
	got, err := svc.OpenOnCable(context.Background(), owner())
	if err != nil {
		t.Fatal(err)
	}
	if !got.Opened || got.Device != domain.CableiPhone || got.URL != "http://172.20.10.2:8080/?r=main" {
		t.Fatalf("%+v", got)
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

const ioregGenericAndroid = `
+-o Android@00300000  <class IOUSBHostDevice, id 0x4, registered, matched, active, busy 0 (0 ms), retain 12>
    {
      "USB Product Name" = "Android"
      "USB Vendor Name" = "Android"
    }
`

const ioregBillboard = `
+-o USB 2.0 BILLBOARD@03100000  <class IOUSBHostDevice, id 0x3, registered, matched, active, busy 0 (0 ms), retain 10>
    {
      "USB Product Name" = "USB 2.0 BILLBOARD"
      "USB Vendor Name" = "VIA Technologies Inc."
    }
`
