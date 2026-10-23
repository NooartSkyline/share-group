package network

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"lanshare/internal/domain"
	"lanshare/internal/usecase/fake"
	"lanshare/internal/usecase/port"
)

func TestParseAndroidLsLine(t *testing.T) {
	dir, ok := parseAndroidLsLine("drwxrws--- 2 u0_a305 media_rw     20480 2026-07-28 14:54 100 Amazing Ultra HD 4K Wallpapers 3840x2160 Px/")
	if !ok || !dir.Dir || dir.Name != "100 Amazing Ultra HD 4K Wallpapers 3840x2160 Px" {
		t.Fatalf("%+v ok=%v", dir, ok)
	}
	file, ok := parseAndroidLsLine("-rwxrwx--- 1 u0_a305 media_rw  35644993 2026-08-12 15:36 672b67b03d85491daab457367233da75.apk")
	if !ok || file.Dir || file.Size != 35644993 || file.Name != "672b67b03d85491daab457367233da75.apk" {
		t.Fatalf("%+v ok=%v", file, ok)
	}
	thai, ok := parseAndroidLsLine("drwxrws--- 3 u0_a305 media_rw      3452 2026-07-28 19:09 แบบบ้าน/")
	if !ok || !thai.Dir || thai.Name != "แบบบ้าน" {
		t.Fatalf("%+v ok=%v", thai, ok)
	}
	if _, ok := parseAndroidLsLine("total 194565"); ok {
		t.Fatal("total line")
	}
	if _, ok := parseAndroidLsLine("drwxrws--- 2 u0_a305 media_rw 3452 2025-10-21 12:57 .anydesk/"); ok {
		t.Fatal("hidden")
	}
}

func TestSplitCablePathRejectsDotDot(t *testing.T) {
	if _, _, _, err := splitCablePath("android/download/../pictures"); err != domain.ErrInvalidInput {
		t.Fatalf("err=%v", err)
	}
	device, root, rel, err := splitCablePath("android/download/แบบบ้าน/a.jpg")
	if err != nil || device != domain.CableAndroid || root != "download" || rel != "แบบบ้าน/a.jpg" {
		t.Fatalf("%s %s %s %v", device, root, rel, err)
	}
	if _, _, _, err := splitCablePath("download/a.jpg"); err != domain.ErrInvalidInput {
		t.Errorf("missing device = %v", err)
	}
}

func TestAndroidAbsStaysInRoot(t *testing.T) {
	abs, err := androidAbs("download", "foo.jpg")
	if err != nil || abs != "/sdcard/Download/foo.jpg" {
		t.Fatalf("%s %v", abs, err)
	}
	if _, err := androidAbs("nope", ""); err != domain.ErrInvalidInput {
		t.Fatalf("err=%v", err)
	}
}

func TestListCableFSForbiddenAndIdle(t *testing.T) {
	svc := NewService(netList{{Name: "en0", IP: "192.168.1.23", Kind: domain.NetPhysical}}, 8080, "")
	member := domain.Device{Role: domain.RoleMember, RoomID: "main"}
	if _, err := svc.ListCableFS(context.Background(), member, ""); err != domain.ErrForbidden {
		t.Errorf("List = %v", err)
	}
	if _, err := svc.ListCableFS(context.Background(), owner(), ""); err != domain.ErrNotFound {
		t.Errorf("idle List = %v", err)
	}
}

func TestListCableFSAndroidRootsAndFolder(t *testing.T) {
	run := fake.NewRunner()
	run.On("/usr/sbin/networksetup -listallhardwareports", fake.RunResult{Out: []byte("Hardware Port: Wi-Fi\nDevice: en0\n")})
	run.On("/usr/sbin/ioreg -r -c IOUSBHostDevice -l -d 1 -w 0", fake.RunResult{Out: []byte(ioregPixel)})
	run.On("adb version", fake.RunResult{Out: []byte("Android Debug Bridge version 1.0.41")})
	run.On("adb devices", fake.RunResult{Out: []byte("List of devices attached\nR58M123\tdevice\n")})
	run.On("adb reverse tcp:8080 tcp:8080", fake.RunResult{})
	run.On("adb shell ls -lp -- '/sdcard/Download'", fake.RunResult{Out: []byte("" +
		"total 8\n" +
		"drwxrws--- 3 u0_a305 media_rw 3452 2026-07-28 19:09 แบบบ้าน/\n" +
		"-rwxrwx--- 1 u0_a305 media_rw 1234 2026-08-12 15:36 note.txt\n")})
	svc := NewService(netList{{Name: "en0", IP: "192.168.1.23", Kind: domain.NetPhysical}}, 8080, "")
	svc.SetRunner(run)

	root, err := svc.ListCableFS(context.Background(), owner(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(root.Entries) != 1 || root.Entries[0].Path != domain.CableAndroid || !root.Entries[0].Dir {
		t.Fatalf("devices = %+v", root)
	}

	device, err := svc.ListCableFS(context.Background(), owner(), "android")
	if err != nil {
		t.Fatal(err)
	}
	if device.Device != domain.CableAndroid || len(device.Entries) < 3 || !device.Entries[0].Dir {
		t.Fatalf("%+v", device)
	}
	if device.Entries[0].Path != "android/apps" || device.Entries[0].Kind != "folder" {
		t.Fatalf("apps root = %+v", device.Entries[0])
	}
	if device.Entries[1].Path != "android/download" {
		t.Fatalf("root path = %+v", device.Entries[1])
	}

	folder, err := svc.ListCableFS(context.Background(), owner(), "android/download")
	if err != nil {
		t.Fatal(err)
	}
	if folder.Title != "ดาวน์โหลด" || len(folder.Entries) != 2 {
		t.Fatalf("%+v", folder)
	}
	if !folder.Entries[0].Dir || folder.Entries[0].Path != "android/download/แบบบ้าน" {
		t.Fatalf("dir = %+v", folder.Entries[0])
	}
	if folder.Entries[1].Dir || folder.Entries[1].Size != 1234 || folder.Entries[1].Path != "android/download/note.txt" {
		t.Fatalf("file = %+v", folder.Entries[1])
	}
}

func TestListCableFSBothDevices(t *testing.T) {
	run := fake.NewRunner()
	run.On("/usr/sbin/networksetup -listallhardwareports", fake.RunResult{Out: []byte("Hardware Port: Wi-Fi\nDevice: en0\n")})
	run.On("/usr/sbin/ioreg -r -c IOUSBHostDevice -l -d 1 -w 0", fake.RunResult{Out: []byte(ioregPixel)})
	run.On("adb version", fake.RunResult{Out: []byte("Android Debug Bridge version 1.0.41")})
	run.On("adb devices", fake.RunResult{Out: []byte("List of devices attached\nR58M123\tdevice\n")})
	run.On("adb reverse tcp:8080 tcp:8080", fake.RunResult{})
	run.On("idevice_id -l", fake.RunResult{Out: []byte("00008140-000545891AE1801C\n")})
	svc := NewService(netList{{Name: "en0", IP: "192.168.1.23", Kind: domain.NetPhysical}}, 8080, "")
	svc.SetRunner(run)

	root, err := svc.ListCableFS(context.Background(), owner(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(root.Entries) != 2 {
		t.Fatalf("devices = %+v", root.Entries)
	}
	if root.Entries[0].Path != domain.CableAndroid || root.Entries[1].Path != domain.CableiPhone {
		t.Fatalf("order = %+v", root.Entries)
	}
}

func TestFirstPhysicalUDIDWiredOnly(t *testing.T) {
	const wifiPaired = `{"result":{"devices":[
		{"connectionProperties":{"transportType":"sameMachine","pairingState":"paired"},"hardwareProperties":{"reality":"simulated","udid":"SIM-1"}},
		{"connectionProperties":{"transportType":"localNetwork","pairingState":"paired","tunnelState":"disconnected"},"hardwareProperties":{"reality":"physical","udid":"00008140-000545891AE1801C"}}
	]}}`
	if id := firstPhysicalUDID([]byte(wifiPaired)); id != "" {
		t.Fatalf("wifi/sim = %q", id)
	}
	const usb = `{"result":{"devices":[
		{"connectionProperties":{"transportType":"localNetwork","pairingState":"paired"},"hardwareProperties":{"reality":"physical","udid":"WIFI-UDID"}},
		{"connectionProperties":{"transportType":"wired","pairingState":"paired"},"hardwareProperties":{"reality":"physical","udid":"00008110-001A4D123456789A"}}
	]}}`
	if id := firstPhysicalUDID([]byte(usb)); id != "00008110-001A4D123456789A" {
		t.Fatalf("wired = %q", id)
	}
}

func TestListCableFSOmitsNetworkPairedIPhone(t *testing.T) {
	run := fake.NewRunner()
	run.On("/usr/sbin/networksetup -listallhardwareports", fake.RunResult{Out: []byte("Hardware Port: Wi-Fi\nDevice: en0\n")})
	run.On("/usr/sbin/ioreg -r -c IOUSBHostDevice -l -d 1 -w 0", fake.RunResult{Out: []byte(ioregPixel)})
	run.On("adb version", fake.RunResult{Out: []byte("Android Debug Bridge version 1.0.41")})
	run.On("adb devices", fake.RunResult{Out: []byte("List of devices attached\nRFCWA19V99W\tdevice\nc3782355\tdevice\n")})
	run.On("adb -s RFCWA19V99W reverse tcp:8080 tcp:8080", fake.RunResult{})
	run.On("adb -s c3782355 reverse tcp:8080 tcp:8080", fake.RunResult{})
	run.On("idevice_id -l", fake.RunResult{})
	run.On("xcrun devicectl list devices --json-output -", fake.RunResult{Out: []byte(`{"result":{"devices":[
		{"connectionProperties":{"transportType":"localNetwork","pairingState":"paired","tunnelState":"disconnected"},"hardwareProperties":{"reality":"physical","udid":"00008140-000545891AE1801C"}}
	]}}`)})
	svc := NewService(netList{{Name: "en0", IP: "192.168.1.23", Kind: domain.NetPhysical}}, 8080, "")
	svc.SetRunner(run)

	root, err := svc.ListCableFS(context.Background(), owner(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(root.Entries) != 2 {
		t.Fatalf("devices = %+v", root.Entries)
	}
	for _, e := range root.Entries {
		if e.Path == domain.CableiPhone || e.Path == domain.CableiPad {
			t.Fatalf("network iPhone listed: %+v", root.Entries)
		}
	}
}

func TestSanitizeCableZipPaths(t *testing.T) {
	got, err := SanitizeCableZipPaths([]string{"android/download/a.jpg", " android/download/a.jpg", "android/download/b.png"})
	if err != nil || len(got) != 2 || got[0] != "android/download/a.jpg" || got[1] != "android/download/b.png" {
		t.Fatalf("%v %v", got, err)
	}
	root, err := SanitizeCableZipPaths([]string{"android/download"})
	if err != nil || len(root) != 1 || root[0] != "android/download" {
		t.Fatalf("folder root = %v %v", root, err)
	}
	if _, err := SanitizeCableZipPaths([]string{"android/download/../x"}); err != domain.ErrInvalidInput {
		t.Errorf("dotdot = %v", err)
	}
	empty, err := SanitizeCableZipPaths(nil)
	if err != nil || len(empty) != 0 {
		t.Errorf("empty = %v %v", empty, err)
	}
	many := make([]string, maxCableZipFiles+1)
	for i := range many {
		many[i] = "android/download/f" + strconv.Itoa(i)
	}
	if _, err := SanitizeCableZipPaths(many); err != domain.ErrTooManyItems {
		t.Errorf("too many = %v", err)
	}
}

func TestCheckCableFilesForbiddenAndIdle(t *testing.T) {
	svc := NewService(netList{{Name: "en0", IP: "192.168.1.23", Kind: domain.NetPhysical}}, 8080, "")
	member := domain.Device{Role: domain.RoleMember, RoomID: "main"}
	if _, err := svc.CheckCableFiles(context.Background(), member, []string{"android/download/a.jpg"}); err != domain.ErrForbidden {
		t.Errorf("member = %v", err)
	}
	if _, err := svc.CheckCableFiles(context.Background(), owner(), []string{"android/download/a.jpg"}); err != domain.ErrNotFound {
		t.Errorf("idle = %v", err)
	}
}

func TestDeleteCableItems(t *testing.T) {
	run := fake.NewRunner()
	run.On("/usr/sbin/networksetup -listallhardwareports", fake.RunResult{Out: []byte("Hardware Port: Wi-Fi\nDevice: en0\n")})
	run.On("/usr/sbin/ioreg -r -c IOUSBHostDevice -l -d 1 -w 0", fake.RunResult{Out: []byte(ioregPixel)})
	run.On("adb version", fake.RunResult{Out: []byte("Android Debug Bridge version 1.0.41")})
	run.On("adb devices", fake.RunResult{Out: []byte("List of devices attached\nR58M123\tdevice\n")})
	run.On("adb reverse tcp:8080 tcp:8080", fake.RunResult{})
	run.On("adb shell rm -rf -- '/sdcard/Download/note.txt'", fake.RunResult{})
	run.On("adb shell rm -rf -- '/sdcard/Download/แบบบ้าน'", fake.RunResult{})
	svc := NewService(netList{{Name: "en0", IP: "192.168.1.23", Kind: domain.NetPhysical}}, 8080, "")
	svc.SetRunner(run)

	member := domain.Device{Role: domain.RoleMember, RoomID: "main"}
	if _, err := svc.DeleteCableItems(context.Background(), member, []string{"android/download/note.txt"}); err != domain.ErrForbidden {
		t.Errorf("member = %v", err)
	}
	if _, err := svc.DeleteCableItems(context.Background(), owner(), []string{"android/download"}); err != domain.ErrInvalidInput {
		t.Errorf("root = %v", err)
	}
	n, err := svc.DeleteCableItems(context.Background(), owner(), []string{"android/download/note.txt", "android/download/แบบบ้าน"})
	if err != nil || n != 2 {
		t.Fatalf("delete = %d %v", n, err)
	}
}

func TestSanitizeCableFolderName(t *testing.T) {
	got, err := sanitizeCableFolderName("  โฟลเดอร์ใหม่  ")
	if err != nil || got != "โฟลเดอร์ใหม่" {
		t.Fatalf("%s %v", got, err)
	}
	for _, name := range []string{"", ".", "..", "a/b", "a\\b", strings.Repeat("x", 81)} {
		if _, err := sanitizeCableFolderName(name); err != domain.ErrInvalidInput {
			t.Errorf("%q = %v", name, err)
		}
	}
}

func TestMkdirCableFolder(t *testing.T) {
	run := fake.NewRunner()
	run.On("/usr/sbin/networksetup -listallhardwareports", fake.RunResult{Out: []byte("Hardware Port: Wi-Fi\nDevice: en0\n")})
	run.On("/usr/sbin/ioreg -r -c IOUSBHostDevice -l -d 1 -w 0", fake.RunResult{Out: []byte(ioregPixel)})
	run.On("adb version", fake.RunResult{Out: []byte("Android Debug Bridge version 1.0.41")})
	run.On("adb devices", fake.RunResult{Out: []byte("List of devices attached\nR58M123\tdevice\n")})
	run.On("adb reverse tcp:8080 tcp:8080", fake.RunResult{})
	run.On("adb shell mkdir -p -- '/sdcard/Download/โฟลเดอร์ใหม่'", fake.RunResult{})
	svc := NewService(netList{{Name: "en0", IP: "192.168.1.23", Kind: domain.NetPhysical}}, 8080, "")
	svc.SetRunner(run)

	member := domain.Device{Role: domain.RoleMember, RoomID: "main"}
	if _, err := svc.MkdirCableFolder(context.Background(), member, "android/download", "โฟลเดอร์ใหม่"); err != domain.ErrForbidden {
		t.Errorf("member = %v", err)
	}
	if _, err := svc.MkdirCableFolder(context.Background(), owner(), "android", "โฟลเดอร์ใหม่"); err != domain.ErrInvalidInput {
		t.Errorf("device root = %v", err)
	}
	if _, err := svc.MkdirCableFolder(context.Background(), owner(), "android/apps", "โฟลเดอร์ใหม่"); err != domain.ErrInvalidInput {
		t.Errorf("apps = %v", err)
	}
	got, err := svc.MkdirCableFolder(context.Background(), owner(), "android/download", "โฟลเดอร์ใหม่")
	if err != nil || got != "android/download/โฟลเดอร์ใหม่" {
		t.Fatalf("mkdir = %s %v", got, err)
	}
}

func TestAndroidShellQuote(t *testing.T) {
	if got := androidShellQuote(`/sdcard/Download/a'b.jpg`); got != `'/sdcard/Download/a'\''b.jpg'` {
		t.Fatalf("%q", got)
	}
}

func TestPlanCableZipFolderWalk(t *testing.T) {
	run := fake.NewRunner()
	run.On("/usr/sbin/networksetup -listallhardwareports", fake.RunResult{Out: []byte("Hardware Port: Wi-Fi\nDevice: en0\n")})
	run.On("/usr/sbin/ioreg -r -c IOUSBHostDevice -l -d 1 -w 0", fake.RunResult{Out: []byte(ioregPixel)})
	run.On("adb version", fake.RunResult{Out: []byte("Android Debug Bridge version 1.0.41")})
	run.On("adb devices", fake.RunResult{Out: []byte("List of devices attached\nR58M123\tdevice\n")})
	run.On("adb reverse tcp:8080 tcp:8080", fake.RunResult{})
	run.On("adb shell ls -lp -- '/sdcard/Download'", fake.RunResult{Out: []byte("" +
		"total 8\n" +
		"drwxrws--- 3 u0_a305 media_rw 3452 2026-07-28 19:09 แบบบ้าน/\n" +
		"-rwxrwx--- 1 u0_a305 media_rw 1234 2026-08-12 15:36 note.txt\n")})
	run.On("adb shell ls -lp -- '/sdcard/Download/แบบบ้าน'", fake.RunResult{Out: []byte("" +
		"total 4\n" +
		"-rwxrwx--- 1 u0_a305 media_rw 99 2026-08-12 15:36 plan.jpg\n")})
	run.On("adb shell ls -lp -- '/sdcard/Download/ว่าง'", fake.RunResult{Out: []byte("total 0\n")})
	svc := NewService(netList{{Name: "en0", IP: "192.168.1.23", Kind: domain.NetPhysical}}, 8080, "")
	svc.SetRunner(run)

	got, err := svc.PlanCableZip(context.Background(), owner(), nil, []string{"android/download"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("%+v", got)
	}
	if got[0].Loc != "android/download/แบบบ้าน/plan.jpg" || got[0].Name != "ดาวน์โหลด/แบบบ้าน/plan.jpg" {
		t.Fatalf("nested = %+v", got[0])
	}
	if got[1].Loc != "android/download/note.txt" || got[1].Name != "ดาวน์โหลด/note.txt" {
		t.Fatalf("file = %+v", got[1])
	}

	if _, err := svc.PlanCableZip(context.Background(), owner(), nil, []string{"android/download/ว่าง"}); err != domain.ErrInvalidInput {
		t.Fatalf("empty folder err = %v, want invalid", err)
	}

	if _, err := svc.PlanCableZip(context.Background(), owner(), nil, []string{"android/download/ลับ"}); err != domain.ErrNotFound {
		t.Fatalf("unreadable folder err = %v, want not found", err)
	}
}

func TestPlanCableZipManyFiles(t *testing.T) {
	var ls strings.Builder
	ls.WriteString("total 250\n")
	for i := 0; i < 250; i++ {
		ls.WriteString("-rw-rw---- 1 u0_a305 media_rw 10 2026-10-23 09:46 f")
		ls.WriteString(strconv.Itoa(i))
		ls.WriteString(".jpg\n")
	}
	run := fake.NewRunner()
	run.On("/usr/sbin/networksetup -listallhardwareports", fake.RunResult{Out: []byte("Hardware Port: Wi-Fi\nDevice: en0\n")})
	run.On("/usr/sbin/ioreg -r -c IOUSBHostDevice -l -d 1 -w 0", fake.RunResult{Out: []byte(ioregPixel)})
	run.On("adb version", fake.RunResult{Out: []byte("Android Debug Bridge version 1.0.41")})
	run.On("adb devices", fake.RunResult{Out: []byte("List of devices attached\nR58M123\tdevice\n")})
	run.On("adb reverse tcp:8080 tcp:8080", fake.RunResult{})
	run.On("adb shell ls -lp -- '/sdcard/Download/album'", fake.RunResult{Out: []byte(ls.String())})
	svc := NewService(netList{{Name: "en0", IP: "192.168.1.23", Kind: domain.NetPhysical}}, 8080, "")
	svc.SetRunner(run)

	got, err := svc.PlanCableZip(context.Background(), owner(), nil, []string{"android/download/album"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 250 {
		t.Fatalf("n=%d, want 250", len(got))
	}
}

type pullToDiskRunner struct {
	inner *fake.Runner
}

func (r *pullToDiskRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	out, err := r.inner.Run(ctx, name, args...)
	if err != nil {
		return out, err
	}
	if name != "adb" && !strings.HasSuffix(name, "/adb") {
		return out, err
	}
	local := ""
	for i, a := range args {
		if a == "pull" && i+2 < len(args) {
			local = args[len(args)-1]
			break
		}
	}
	if local == "" {
		return out, err
	}
	if err := os.MkdirAll(local, 0o700); err != nil {
		return out, err
	}
	if err := os.WriteFile(filepath.Join(local, "a.jpg"), []byte("aa"), 0o600); err != nil {
		return out, err
	}
	sub := filepath.Join(local, "sub")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		return out, err
	}
	if err := os.WriteFile(filepath.Join(sub, "b.jpg"), []byte("bb"), 0o600); err != nil {
		return out, err
	}
	return out, err
}

func TestStageCableZipPullsFolderOnce(t *testing.T) {
	inner := fake.NewRunner()
	inner.On("/usr/sbin/networksetup -listallhardwareports", fake.RunResult{Out: []byte("Hardware Port: Wi-Fi\nDevice: en0\n")})
	inner.On("/usr/sbin/ioreg -r -c IOUSBHostDevice -l -d 1 -w 0", fake.RunResult{Out: []byte(ioregPixel)})
	inner.On("adb version", fake.RunResult{Out: []byte("Android Debug Bridge version 1.0.41")})
	inner.On("adb devices", fake.RunResult{Out: []byte("List of devices attached\nR58M123\tdevice\n")})
	inner.On("adb reverse tcp:8080 tcp:8080", fake.RunResult{})
	inner.On("adb", fake.RunResult{})
	svc := NewService(netList{{Name: "en0", IP: "192.168.1.23", Kind: domain.NetPhysical}}, 8080, "")
	svc.SetRunner(&pullToDiskRunner{inner: inner})

	dir, items, err := svc.StageCableZip(context.Background(), owner(), nil, []string{"android/download"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if len(items) != 2 {
		t.Fatalf("items=%d %+v", len(items), items)
	}
	pulls := 0
	for _, c := range inner.Calls() {
		line := strings.Join(append([]string{c.Name}, c.Args...), " ")
		if !strings.Contains(line, " pull ") {
			continue
		}
		pulls++
		if !strings.Contains(line, "/sdcard/Download") {
			t.Fatalf("pull = %s", line)
		}
		if strings.Contains(line, "a.jpg") {
			t.Fatalf("pulled a file instead of the folder: %s", line)
		}
	}
	if pulls != 1 {
		t.Fatalf("pulls=%d, want 1", pulls)
	}
}

func TestCableLocCovered(t *testing.T) {
	folders := []string{"android/download/album"}
	if !cableLocCovered("android/download/album/a.jpg", folders) {
		t.Fatal("child")
	}
	if cableLocCovered("android/pictures/a.jpg", folders) {
		t.Fatal("other")
	}
}

func TestPeekCableFileAndroid(t *testing.T) {
	run := fake.NewRunner()
	run.On("/usr/sbin/networksetup -listallhardwareports", fake.RunResult{Out: []byte("Hardware Port: Wi-Fi\nDevice: en0\n")})
	run.On("/usr/sbin/ioreg -r -c IOUSBHostDevice -l -d 1 -w 0", fake.RunResult{Out: []byte(ioregPixel)})
	run.On("adb version", fake.RunResult{Out: []byte("Android Debug Bridge version 1.0.41")})
	run.On("adb devices", fake.RunResult{Out: []byte("List of devices attached\nR58M123\tdevice\n")})
	run.On("adb reverse tcp:8080 tcp:8080", fake.RunResult{})
	run.On("adb exec-out dd if=/sdcard/Download/note.txt bs=16384 count=1", fake.RunResult{Out: []byte("hello peek")})
	svc := NewService(netList{{Name: "en0", IP: "192.168.1.23", Kind: domain.NetPhysical}}, 8080, "")
	svc.SetRunner(run)
	got, err := svc.PeekCableFile(context.Background(), owner(), "android/download/note.txt", 0)
	if err != nil || got.Name != "note.txt" || string(got.Data) != "hello peek" {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := svc.PeekCableFile(context.Background(), domain.Device{Role: domain.RoleMember, RoomID: "main"}, "android/download/note.txt", 0); err != domain.ErrForbidden {
		t.Errorf("member peek = %v", err)
	}
	if _, err := svc.PeekCableFile(context.Background(), owner(), "android/download/../x", 0); err != domain.ErrInvalidInput {
		t.Errorf("dotdot peek = %v", err)
	}
}

func TestOpenCableFileRejectsRoot(t *testing.T) {
	svc := NewService(netList{{Name: "en0", IP: "192.168.1.23", Kind: domain.NetPhysical}}, 8080, "")
	run := fake.NewRunner()
	run.On("/usr/sbin/networksetup -listallhardwareports", fake.RunResult{Out: []byte("Hardware Port: Wi-Fi\nDevice: en0\n")})
	run.On("/usr/sbin/ioreg -r -c IOUSBHostDevice -l -d 1 -w 0", fake.RunResult{Out: []byte(ioregPixel)})
	run.On("adb version", fake.RunResult{Out: []byte("Android Debug Bridge version 1.0.41")})
	run.On("adb devices", fake.RunResult{Out: []byte("List of devices attached\nR58M123\tdevice\n")})
	run.On("adb reverse tcp:8080 tcp:8080", fake.RunResult{})
	svc.SetRunner(run)
	if _, err := svc.OpenCableFile(context.Background(), owner(), "android/download"); err != domain.ErrInvalidInput {
		t.Errorf("Open root file = %v", err)
	}
}

func TestParseAndroidPackagesAndPmPath(t *testing.T) {
	pkgs := parseAndroidPackages([]byte("Error: java.lang.SecurityException\npackage:com.foo.app\npackage:com.foo.app\npackage:bad\npackage:apitester.org\n"))
	if len(pkgs) != 2 || pkgs[0] != "apitester.org" || pkgs[1] != "com.foo.app" {
		t.Fatalf("%v", pkgs)
	}
	paths := parsePmPath([]byte("package:/data/app/~~x==/com.foo.app-hash/base.apk\npackage:/data/app/~~x==/com.foo.app-hash/split_config.th.apk\npackage:/tmp/../etc/passwd\n"))
	if len(paths) != 2 || pathBase(paths[0]) != "base.apk" || pathBase(paths[1]) != "split_config.th.apk" {
		t.Fatalf("%v", paths)
	}
}

func TestAndroidCopyPackages(t *testing.T) {
	src, pkgs, err := androidCopyPackages([]string{"android/apps/com.foo.app", "android/apps/com.foo.app/base.apk"})
	if err != nil || src != domain.CableAndroid || len(pkgs) != 1 || pkgs[0] != "com.foo.app" {
		t.Fatalf("%s %v %v", src, pkgs, err)
	}
	if _, _, err := androidCopyPackages([]string{"android/download/a.jpg"}); err != domain.ErrInvalidInput {
		t.Errorf("files = %v", err)
	}
	if _, _, err := androidCopyPackages([]string{"iphone/apps/com.foo.app"}); err != domain.ErrInvalidInput {
		t.Errorf("ios = %v", err)
	}
}

func TestPasteCableAppsSameDevice(t *testing.T) {
	run := fake.NewRunner()
	run.On("/usr/sbin/networksetup -listallhardwareports", fake.RunResult{Out: []byte("Hardware Port: Wi-Fi\nDevice: en0\n")})
	run.On("/usr/sbin/ioreg -r -c IOUSBHostDevice -l -d 1 -w 0", fake.RunResult{Out: []byte(ioregPixel)})
	run.On("adb version", fake.RunResult{Out: []byte("Android Debug Bridge version 1.0.41")})
	run.On("adb devices", fake.RunResult{Out: []byte("List of devices attached\nR58M123\tdevice\n")})
	run.On("adb reverse tcp:8080 tcp:8080", fake.RunResult{})
	svc := NewService(netList{{Name: "en0", IP: "192.168.1.23", Kind: domain.NetPhysical}}, 8080, "")
	svc.SetRunner(run)
	svc.clip = &cableAppClip{kind: cableClipApps, source: domain.CableAndroid, names: []string{"com.foo.app"}, pkgs: []cableAppPkg{{ID: "com.foo.app", Name: "com.foo.app"}}}
	if _, err := svc.PasteCableApps(context.Background(), owner(), domain.CableAndroid); err != domain.ErrInvalidInput {
		t.Fatalf("same device = %v", err)
	}
	if _, err := svc.PasteCableApps(context.Background(), domain.Device{Role: domain.RoleMember, RoomID: "main"}, "android:OTHER"); err != domain.ErrForbidden {
		t.Errorf("member = %v", err)
	}
}

func TestPasteAcrossDevices(t *testing.T) {
	serials := []string{"RFCWA19V99W", "c3782355"}
	if pasteAcrossDevices("android:RFCWA19V99W", "android:RFCWA19V99W", serials) {
		t.Fatal("same android")
	}
	if !cablePasteAllowed("android:RFCWA19V99W", "android:RFCWA19V99W", serials, true) {
		t.Fatal("same android files")
	}
	if !pasteAcrossDevices("android:RFCWA19V99W", "android:c3782355", serials) {
		t.Fatal("other android")
	}
	if !pasteAcrossDevices("iphone", "android:c3782355", serials) {
		t.Fatal("ios to android")
	}
	if pasteAcrossDevices("android:RFCWA19V99W", "iphone", serials) {
		t.Fatal("to ios")
	}
	if !cablePasteAllowed(domain.CableHost, domain.CableHost, serials, true) {
		t.Fatal("host files")
	}
	if cablePasteAllowed(domain.CableHost, domain.CableHost, serials, false) {
		t.Fatal("host apps")
	}
	if !cablePasteAllowed("android:RFCWA19V99W", domain.CableHost, serials, true) {
		t.Fatal("android to host")
	}
	if !cablePasteAllowed(domain.CableHost, "android:RFCWA19V99W", serials, true) {
		t.Fatal("host to android")
	}
	if pasteAcrossDevices(domain.CableHost, domain.CableiPhone, serials) {
		t.Fatal("host to ios")
	}
}

func TestCutPasteOverlap(t *testing.T) {
	serials := []string{"RFCWA19V99W"}
	origin := "android:RFCWA19V99W/download/note.txt"
	if !cutPasteSameFolder([]string{origin}, "android:RFCWA19V99W/download", serials) {
		t.Fatal("same folder")
	}
	if cutPasteNested([]string{origin}, "android:RFCWA19V99W/download", serials) {
		t.Fatal("same folder is not nested")
	}
	if cutPasteSameFolder([]string{origin}, "android:RFCWA19V99W/pictures", serials) || cutPasteNested([]string{origin}, "android:RFCWA19V99W/pictures", serials) {
		t.Fatal("other folder")
	}
	if !cutPasteNested([]string{"android:RFCWA19V99W/download/foo"}, "android:RFCWA19V99W/download/foo/bar", serials) {
		t.Fatal("inside cut folder")
	}
	if !cutPasteSameFolder([]string{"android/download/note.txt"}, "android/download", nil) {
		t.Fatal("bare android")
	}
}

func TestUniquifyClipFiles(t *testing.T) {
	taken := map[string]struct{}{"note.txt": {}, "foo": {}}
	files := uniquifyClipFiles([]cableClipFile{
		{Name: "note.txt", Path: "/tmp/note.txt"},
		{Name: "foo/a.jpg", Path: "/tmp/a.jpg"},
		{Name: "foo/b.jpg", Path: "/tmp/b.jpg"},
		{Name: "fresh.txt", Path: "/tmp/fresh.txt"},
	}, taken)
	if files[0].Name != "note (2).txt" {
		t.Fatalf("file = %s", files[0].Name)
	}
	if files[1].Name != "foo (2)/a.jpg" || files[2].Name != "foo (2)/b.jpg" {
		t.Fatalf("folder = %s %s", files[1].Name, files[2].Name)
	}
	if files[3].Name != "fresh.txt" {
		t.Fatalf("free = %s", files[3].Name)
	}
}

func TestClipLocalPath(t *testing.T) {
	got, err := clipLocalPath("/tmp/clip", "โฟลเดอร์/a.jpg")
	if err != nil || got != filepath.Join("/tmp/clip", "โฟลเดอร์", "a.jpg") {
		t.Fatalf("%s %v", got, err)
	}
	if _, err := clipLocalPath("/tmp/clip", "../etc/passwd"); err != domain.ErrInvalidInput {
		t.Fatalf("dotdot = %v", err)
	}
}

func TestCopyCableItemsRejectsMixed(t *testing.T) {
	run := fake.NewRunner()
	run.On("/usr/sbin/networksetup -listallhardwareports", fake.RunResult{Out: []byte("Hardware Port: Wi-Fi\nDevice: en0\n")})
	run.On("/usr/sbin/ioreg -r -c IOUSBHostDevice -l -d 1 -w 0", fake.RunResult{Out: []byte(ioregPixel)})
	run.On("adb version", fake.RunResult{Out: []byte("Android Debug Bridge version 1.0.41")})
	run.On("adb devices", fake.RunResult{Out: []byte("List of devices attached\nR58M123\tdevice\n")})
	run.On("adb reverse tcp:8080 tcp:8080", fake.RunResult{})
	svc := NewService(netList{{Name: "en0", IP: "192.168.1.23", Kind: domain.NetPhysical}}, 8080, "")
	svc.SetRunner(run)
	_, err := svc.CopyCableItems(context.Background(), owner(), []string{"android/download/a.jpg"}, []string{"android/apps/com.foo.app"}, false)
	if err != domain.ErrInvalidInput {
		t.Fatalf("mixed = %v", err)
	}
	if _, err := svc.CopyCableItems(context.Background(), domain.Device{Role: domain.RoleMember, RoomID: "main"}, []string{"android/download/a.jpg"}, nil, false); err != domain.ErrForbidden {
		t.Errorf("member = %v", err)
	}
	if _, err := svc.CopyCableItems(context.Background(), owner(), nil, []string{"android/apps/com.foo.app"}, true); err != domain.ErrInvalidInput {
		t.Fatalf("cut apps = %v", err)
	}
}

func TestPasteCableFilesToOtherAndroid(t *testing.T) {
	run := fake.NewRunner()
	run.On("/usr/sbin/networksetup -listallhardwareports", fake.RunResult{Out: []byte("Hardware Port: Wi-Fi\nDevice: en0\n")})
	run.On("/usr/sbin/ioreg -r -c IOUSBHostDevice -l -d 1 -w 0", fake.RunResult{Out: []byte(ioregPixel)})
	run.On("adb version", fake.RunResult{Out: []byte("Android Debug Bridge version 1.0.41")})
	run.On("adb devices", fake.RunResult{Out: []byte("List of devices attached\nRFCWA19V99W\tdevice\nc3782355\tdevice\n")})
	run.On("adb", fake.RunResult{})
	svc := NewService(netList{{Name: "en0", IP: "192.168.1.23", Kind: domain.NetPhysical}}, 8080, "")
	svc.SetRunner(run)
	dir := t.TempDir()
	local := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(local, []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	svc.clip = &cableAppClip{
		kind:   cableClipFiles,
		source: "android:RFCWA19V99W",
		names:  []string{"note.txt"},
		files:  []cableClipFile{{Name: "note.txt", Path: local}},
		dir:    dir,
	}
	n, err := svc.PasteCableApps(context.Background(), owner(), "android:c3782355")
	if err != nil || n != 1 {
		t.Fatalf("download n=%d err=%v", n, err)
	}
	n, err = svc.PasteCableApps(context.Background(), owner(), "android:c3782355/pictures")
	if err != nil || n != 1 {
		t.Fatalf("pictures n=%d err=%v", n, err)
	}
	n, err = svc.PasteCableApps(context.Background(), owner(), "android:RFCWA19V99W/dcim")
	if err != nil || n != 1 {
		t.Fatalf("same device n=%d err=%v", n, err)
	}
	pushDL, pushPic, pushSelf := false, false, false
	for _, c := range run.Calls() {
		line := strings.Join(append([]string{c.Name}, c.Args...), " ")
		if strings.Contains(line, "push") && strings.Contains(line, "/sdcard/Download/note.txt") {
			pushDL = true
		}
		if strings.Contains(line, "push") && strings.Contains(line, "/sdcard/Pictures/note.txt") {
			pushPic = true
		}
		if strings.Contains(line, "push") && strings.Contains(line, "/sdcard/DCIM/note.txt") {
			pushSelf = true
		}
	}
	if !pushDL || !pushPic || !pushSelf {
		t.Fatalf("missing push download=%v pictures=%v same=%v", pushDL, pushPic, pushSelf)
	}
}

func TestPasteCutClearsClipboard(t *testing.T) {
	run := fake.NewRunner()
	run.On("/usr/sbin/networksetup -listallhardwareports", fake.RunResult{Out: []byte("Hardware Port: Wi-Fi\nDevice: en0\n")})
	run.On("/usr/sbin/ioreg -r -c IOUSBHostDevice -l -d 1 -w 0", fake.RunResult{Out: []byte(ioregPixel)})
	run.On("adb version", fake.RunResult{Out: []byte("Android Debug Bridge version 1.0.41")})
	run.On("adb devices", fake.RunResult{Out: []byte("List of devices attached\nRFCWA19V99W\tdevice\nc3782355\tdevice\n")})
	run.On("adb", fake.RunResult{})
	svc := NewService(netList{{Name: "en0", IP: "192.168.1.23", Kind: domain.NetPhysical}}, 8080, "")
	svc.SetRunner(run)
	dir := t.TempDir()
	local := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(local, []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	svc.clip = &cableAppClip{
		kind:    cableClipFiles,
		source:  "android:RFCWA19V99W",
		names:   []string{"note.txt"},
		files:   []cableClipFile{{Name: "note.txt", Path: local}},
		dir:     dir,
		cut:     true,
		origins: []string{"android:RFCWA19V99W/download/note.txt"},
	}
	n, err := svc.PasteCableApps(context.Background(), owner(), "android:c3782355/download")
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if svc.clip != nil {
		t.Fatal("cut paste should clear clipboard")
	}
}

func TestPasteCutSameDevice(t *testing.T) {
	run := fake.NewRunner()
	run.On("/usr/sbin/networksetup -listallhardwareports", fake.RunResult{Out: []byte("Hardware Port: Wi-Fi\nDevice: en0\n")})
	run.On("/usr/sbin/ioreg -r -c IOUSBHostDevice -l -d 1 -w 0", fake.RunResult{Out: []byte(ioregPixel)})
	run.On("adb version", fake.RunResult{Out: []byte("Android Debug Bridge version 1.0.41")})
	run.On("adb devices", fake.RunResult{Out: []byte("List of devices attached\nRFCWA19V99W\tdevice\n")})
	run.On("adb reverse tcp:8080 tcp:8080", fake.RunResult{})
	run.On("adb shell ls -lp -- '/sdcard/Download'", fake.RunResult{Out: []byte("-rw-rw---- 1 u0_a305 media_rw 21 2026-10-23 09:46 note.txt\n")})
	run.On("adb", fake.RunResult{})
	svc := NewService(netList{{Name: "en0", IP: "192.168.1.23", Kind: domain.NetPhysical}}, 8080, "")
	svc.SetRunner(run)
	dir := t.TempDir()
	local := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(local, []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	svc.clip = &cableAppClip{
		kind:    cableClipFiles,
		source:  domain.CableAndroid,
		names:   []string{"note.txt"},
		files:   []cableClipFile{{Name: "note.txt", Path: local}},
		dir:     dir,
		cut:     true,
		origins: []string{"android/download/note.txt"},
	}
	n, err := svc.PasteCableApps(context.Background(), owner(), "android/download")
	if err != nil || n != 1 {
		t.Fatalf("same folder n=%d err=%v", n, err)
	}
	if svc.clip == nil {
		t.Fatal("same-folder paste should keep the cut clipboard")
	}
	n, err = svc.PasteCableApps(context.Background(), owner(), "android/pictures")
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if svc.clip != nil {
		t.Fatal("cut paste should clear clipboard")
	}
	copied, moved := false, false
	for _, c := range run.Calls() {
		line := strings.Join(append([]string{c.Name}, c.Args...), " ")
		if strings.Contains(line, "push") && strings.Contains(line, "/sdcard/Download/note (2).txt") {
			copied = true
		}
		if strings.Contains(line, "push") && strings.Contains(line, "/sdcard/Pictures/note.txt") {
			moved = true
		}
	}
	if !copied || !moved {
		t.Fatalf("missing push copy=%v move=%v", copied, moved)
	}
}

func TestListCableFSAndroidApps(t *testing.T) {
	run := fake.NewRunner()
	run.On("/usr/sbin/networksetup -listallhardwareports", fake.RunResult{Out: []byte("Hardware Port: Wi-Fi\nDevice: en0\n")})
	run.On("/usr/sbin/ioreg -r -c IOUSBHostDevice -l -d 1 -w 0", fake.RunResult{Out: []byte(ioregPixel)})
	run.On("adb version", fake.RunResult{Out: []byte("Android Debug Bridge version 1.0.41")})
	run.On("adb devices", fake.RunResult{Out: []byte("List of devices attached\nR58M123\tdevice\n")})
	run.On("adb reverse tcp:8080 tcp:8080", fake.RunResult{})
	run.On("adb shell pm list packages -3 --user 0", fake.RunResult{Out: []byte("package:com.foo.app\npackage:apitester.org\n")})
	run.On("adb shell pm path --user 0 com.foo.app", fake.RunResult{Out: []byte("package:/data/app/x/base.apk\npackage:/data/app/x/split_config.th.apk\n")})
	svc := NewService(netList{{Name: "en0", IP: "192.168.1.23", Kind: domain.NetPhysical}}, 8080, "")
	svc.SetRunner(run)

	list, err := svc.ListCableFS(context.Background(), owner(), "android/apps")
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Entries) != 2 || list.Entries[0].Name != "apitester.org" || !list.Entries[0].Dir || list.Entries[0].Kind != "app" {
		t.Fatalf("%+v", list.Entries)
	}
	if list.Entries[0].Path != "android/apps/apitester.org" {
		t.Fatalf("path = %s", list.Entries[0].Path)
	}

	apks, err := svc.ListCableFS(context.Background(), owner(), "android/apps/com.foo.app")
	if err != nil {
		t.Fatal(err)
	}
	if len(apks.Entries) != 2 || apks.Entries[0].Name != "base.apk" || apks.Entries[0].Dir {
		t.Fatalf("%+v", apks.Entries)
	}
}

func TestParseAdbSerials(t *testing.T) {
	got := parseAdbSerials([]byte("List of devices attached\nRFCWA19V99W\tdevice\nemulator-5554\tdevice\nR58M123\tunauthorized\n"))
	if len(got) != 2 || got[0] != "RFCWA19V99W" || got[1] != "emulator-5554" {
		t.Fatalf("%v", got)
	}
}

func TestParseDevicectlFiles(t *testing.T) {
	raw := []byte(`{"result":{"files":[{"name":"Scan.pdf","isDirectory":false,"size":9001},{"name":"Photos","isDirectory":true}]}}`)
	got, err := parseDevicectlFiles(raw, "iphone/documents")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "Scan.pdf" || got[0].Size != 9001 || got[0].Dir || !got[1].Dir {
		t.Fatalf("%+v", got)
	}
	if got[0].Path != "iphone/documents/Scan.pdf" {
		t.Fatalf("path = %s", got[0].Path)
	}
}

func TestParseDevicectlFilesNested(t *testing.T) {
	raw := []byte(`{"result":{"files":[{
		"name":"Documents","relativePath":"Documents",
		"metadata":{"size":96},"resources":{"isDirectory":true,"isHidden":false}
	},{
		"name":"note.txt","relativePath":"note.txt",
		"metadata":{"size":1234},"resources":{"isDirectory":false}
	},{
		"name":".hidden","resources":{"isDirectory":false,"isHidden":true}
	}]}}`)
	got, err := parseDevicectlFiles(raw, "iphone/th.co.dohome.quickshop.app")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !got[0].Dir || got[0].Name != "Documents" || got[1].Dir || got[1].Size != 1234 {
		t.Fatalf("%+v", got)
	}
	if got[0].Path != "iphone/th.co.dohome.quickshop.app/Documents" {
		t.Fatalf("path = %s", got[0].Path)
	}
}

func TestParseDevicectlApps(t *testing.T) {
	raw := []byte(`{"info":{"outcome":"success"},"result":{"apps":[
		{"name":"QuickShop","bundleIdentifier":"th.co.dohome.quickshop.app","containerAccessible":true},
		{"name":"Hidden","bundleIdentifier":"th.co.skip","containerAccessible":false},
		{"name":"เจ้าเหมียว","bundleIdentifier":"com.dohome.dhwrap.dhWrap","containerAccessible":true}
	]}}`)
	got, err := parseDevicectlApps(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "QuickShop" || got[1].Name != "เจ้าเหมียว" {
		t.Fatalf("%+v", got)
	}
}

func TestListCableFSIOSApps(t *testing.T) {
	const udid = "00008140-000545891AE1801C"
	appsJSON := []byte(`{"info":{"outcome":"success"},"result":{"apps":[
		{"name":"QuickShop","bundleIdentifier":"th.co.dohome.quickshop.app","containerAccessible":true}
	]}}`)
	filesJSON := []byte(`{"info":{"outcome":"success"},"result":{"files":[{
		"name":"Documents","metadata":{"size":96},"resources":{"isDirectory":true}
	}]}}`)
	failJSON := []byte(`{"info":{"outcome":"failed"},"error":{"code":3,"domain":"CoreDevice.ActionError"}}`)
	run := fake.NewRunner()
	run.On("/usr/sbin/networksetup -listallhardwareports", fake.RunResult{Out: []byte("Hardware Port: Wi-Fi\nDevice: en0\n")})
	run.On("/usr/sbin/ioreg -r -c IOUSBHostDevice -l -d 1 -w 0", fake.RunResult{Out: []byte("")})
	run.On("idevice_id -l", fake.RunResult{Out: []byte(udid + "\n")})
	run.On("xcrun devicectl device info apps --device "+udid+" --require-container-access --json-output -", fake.RunResult{Out: appsJSON})
	run.On("xcrun devicectl device info files --device "+udid+" --domain-type appDataContainer --domain-identifier th.co.dohome.quickshop.app --no-recurse --json-output -", fake.RunResult{Out: filesJSON})
	run.On("xcrun devicectl device info files --device "+udid+" --domain-type appDataContainer --domain-identifier com.apple.DocumentsApp --no-recurse --json-output -", fake.RunResult{Out: failJSON})
	svc := NewService(netList{{Name: "en0", IP: "192.168.1.23", Kind: domain.NetPhysical}}, 8080, "")
	svc.SetRunner(run)

	root, err := svc.ListCableFS(context.Background(), owner(), "iphone")
	if err != nil {
		t.Fatal(err)
	}
	if root.Hint != iosHintDevAppsOnly {
		t.Fatalf("hint = %q", root.Hint)
	}
	if len(root.Entries) != 1 || root.Entries[0].Name != "QuickShop" || root.Entries[0].Path != "iphone/th.co.dohome.quickshop.app" {
		t.Fatalf("apps = %+v", root.Entries)
	}

	folder, err := svc.ListCableFS(context.Background(), owner(), "iphone/th.co.dohome.quickshop.app")
	if err != nil {
		t.Fatal(err)
	}
	if folder.Title != "QuickShop" || len(folder.Entries) != 1 || !folder.Entries[0].Dir || folder.Entries[0].Name != "Documents" {
		t.Fatalf("%+v", folder)
	}

	denied, err := svc.ListCableFS(context.Background(), owner(), "iphone/com.apple.DocumentsApp")
	if err != nil {
		t.Fatal(err)
	}
	if denied.Hint != iosHintNoAccess || strings.Contains(denied.Hint, "Developer Mode") {
		t.Fatalf("denied hint = %q", denied.Hint)
	}
	if len(denied.Entries) != 0 {
		t.Fatalf("denied entries = %+v", denied.Entries)
	}
}

func TestListCableFSHostVolumesAndFile(t *testing.T) {
	svc := NewService(netList{{Name: "en0", IP: "192.168.1.23", Kind: domain.NetPhysical}}, 8080, "")
	host := &fake.HostFS{
		Vols: []port.HostVolume{{ID: "download", Label: "ดาวน์โหลด"}},
		Entries: map[string][]port.HostEntry{
			"download\x00": {{Name: "note.txt", Size: 2}},
		},
		Files: map[string][]byte{"download\x00note.txt": []byte("hi")},
	}
	svc.SetHostBrowser(host)

	root, err := svc.ListCableFS(context.Background(), owner(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(root.Entries) != 1 || root.Entries[0].Path != domain.CableHost {
		t.Fatalf("devices = %+v", root.Entries)
	}

	vols, err := svc.ListCableFS(context.Background(), owner(), domain.CableHost)
	if err != nil || len(vols.Entries) != 1 || vols.Entries[0].Name != "ดาวน์โหลด" || vols.Entries[0].Path != "host/download" {
		t.Fatalf("vols = %+v %v", vols.Entries, err)
	}

	list, err := svc.ListCableFS(context.Background(), owner(), "host/download")
	if err != nil || len(list.Entries) != 1 || list.Entries[0].Name != "note.txt" {
		t.Fatalf("list = %+v %v", list, err)
	}

	cf, err := svc.OpenCableFile(context.Background(), owner(), "host/download/note.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer cf.Content.Close()
	if cf.Name != "note.txt" || cf.Size != 2 {
		t.Fatalf("file = %+v", cf)
	}

	made, err := svc.MkdirCableFolder(context.Background(), owner(), "host/download", "ใหม่")
	if err != nil || made != "host/download/ใหม่" {
		t.Fatalf("mkdir = %q %v", made, err)
	}
	if n, err := svc.DeleteCableItems(context.Background(), owner(), []string{"host/download/note.txt"}); err != nil || n != 1 {
		t.Fatalf("delete = %d %v", n, err)
	}
}

func TestPasteCableFilesOntoHost(t *testing.T) {
	svc := NewService(netList{{Name: "en0", IP: "192.168.1.23", Kind: domain.NetPhysical}}, 8080, "")
	host := &fake.HostFS{
		Vols:    []port.HostVolume{{ID: "download", Label: "ดาวน์โหลด"}},
		Entries: map[string][]port.HostEntry{"download\x00": {}},
	}
	svc.SetHostBrowser(host)
	dir := t.TempDir()
	local := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(local, []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	svc.clip = &cableAppClip{
		kind:   cableClipFiles,
		source: domain.CableHost,
		names:  []string{"note.txt"},
		files:  []cableClipFile{{Name: "note.txt", Path: local}},
		dir:    dir,
	}
	n, err := svc.PasteCableApps(context.Background(), owner(), "host/download")
	if err != nil || n != 1 {
		t.Fatalf("host n=%d err=%v", n, err)
	}
	if got := string(host.Files["download\x00note.txt"]); got != "hi" {
		t.Fatalf("wrote %q", got)
	}

	svc.clip = &cableAppClip{
		kind:   cableClipFiles,
		source: "android:RFCWA19V99W",
		names:  []string{"from-phone.txt"},
		files:  []cableClipFile{{Name: "from-phone.txt", Path: local}},
		dir:    dir,
	}
	n, err = svc.PasteCableApps(context.Background(), owner(), "host/download")
	if err != nil || n != 1 {
		t.Fatalf("android to host n=%d err=%v", n, err)
	}

	svc.clip = &cableAppClip{
		kind:   cableClipApps,
		source: domain.CableAndroid,
		names:  []string{"com.foo.app"},
		pkgs:   []cableAppPkg{{ID: "com.foo.app", Name: "com.foo.app"}},
	}
	if _, err := svc.PasteCableApps(context.Background(), owner(), domain.CableHost); err != domain.ErrInvalidInput {
		t.Fatalf("apps on host = %v", err)
	}
}
