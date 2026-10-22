package network

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"lanshare/internal/domain"
	"lanshare/internal/usecase/fake"
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
	if _, _, err := splitCablePath("download/../pictures"); err != domain.ErrInvalidInput {
		t.Fatalf("err=%v", err)
	}
	root, rel, err := splitCablePath("download/แบบบ้าน/a.jpg")
	if err != nil || root != "download" || rel != "แบบบ้าน/a.jpg" {
		t.Fatalf("%s %s %v", root, rel, err)
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
	run.On("adb shell ls -lp -- /sdcard/Download", fake.RunResult{Out: []byte("" +
		"total 8\n" +
		"drwxrws--- 3 u0_a305 media_rw 3452 2026-07-28 19:09 แบบบ้าน/\n" +
		"-rwxrwx--- 1 u0_a305 media_rw 1234 2026-08-12 15:36 note.txt\n")})
	svc := NewService(netList{{Name: "en0", IP: "192.168.1.23", Kind: domain.NetPhysical}}, 8080, "")
	svc.SetRunner(run)

	root, err := svc.ListCableFS(context.Background(), owner(), "")
	if err != nil {
		t.Fatal(err)
	}
	if root.Device != domain.CableAndroid || len(root.Entries) < 3 || !root.Entries[0].Dir {
		t.Fatalf("%+v", root)
	}

	folder, err := svc.ListCableFS(context.Background(), owner(), "download")
	if err != nil {
		t.Fatal(err)
	}
	if folder.Title != "ดาวน์โหลด" || len(folder.Entries) != 2 {
		t.Fatalf("%+v", folder)
	}
	if !folder.Entries[0].Dir || folder.Entries[0].Path != "download/แบบบ้าน" {
		t.Fatalf("dir = %+v", folder.Entries[0])
	}
	if folder.Entries[1].Dir || folder.Entries[1].Size != 1234 || folder.Entries[1].Path != "download/note.txt" {
		t.Fatalf("file = %+v", folder.Entries[1])
	}
}

func TestSanitizeCableZipPaths(t *testing.T) {
	got, err := SanitizeCableZipPaths([]string{"download/a.jpg", " download/a.jpg", "download/b.png"})
	if err != nil || len(got) != 2 || got[0] != "download/a.jpg" || got[1] != "download/b.png" {
		t.Fatalf("%v %v", got, err)
	}
	root, err := SanitizeCableZipPaths([]string{"download"})
	if err != nil || len(root) != 1 || root[0] != "download" {
		t.Fatalf("folder root = %v %v", root, err)
	}
	if _, err := SanitizeCableZipPaths([]string{"download/../x"}); err != domain.ErrInvalidInput {
		t.Errorf("dotdot = %v", err)
	}
	empty, err := SanitizeCableZipPaths(nil)
	if err != nil || len(empty) != 0 {
		t.Errorf("empty = %v %v", empty, err)
	}
	many := make([]string, maxCableZipFiles+1)
	for i := range many {
		many[i] = "download/f" + strconv.Itoa(i)
	}
	if _, err := SanitizeCableZipPaths(many); err != domain.ErrInvalidInput {
		t.Errorf("too many = %v", err)
	}
}

func TestCheckCableFilesForbiddenAndIdle(t *testing.T) {
	svc := NewService(netList{{Name: "en0", IP: "192.168.1.23", Kind: domain.NetPhysical}}, 8080, "")
	member := domain.Device{Role: domain.RoleMember, RoomID: "main"}
	if _, err := svc.CheckCableFiles(context.Background(), member, []string{"download/a.jpg"}); err != domain.ErrForbidden {
		t.Errorf("member = %v", err)
	}
	if _, err := svc.CheckCableFiles(context.Background(), owner(), []string{"download/a.jpg"}); err != domain.ErrNotFound {
		t.Errorf("idle = %v", err)
	}
}

func TestPlanCableZipFolderWalk(t *testing.T) {
	run := fake.NewRunner()
	run.On("/usr/sbin/networksetup -listallhardwareports", fake.RunResult{Out: []byte("Hardware Port: Wi-Fi\nDevice: en0\n")})
	run.On("/usr/sbin/ioreg -r -c IOUSBHostDevice -l -d 1 -w 0", fake.RunResult{Out: []byte(ioregPixel)})
	run.On("adb version", fake.RunResult{Out: []byte("Android Debug Bridge version 1.0.41")})
	run.On("adb devices", fake.RunResult{Out: []byte("List of devices attached\nR58M123\tdevice\n")})
	run.On("adb reverse tcp:8080 tcp:8080", fake.RunResult{})
	run.On("adb shell ls -lp -- /sdcard/Download", fake.RunResult{Out: []byte("" +
		"total 8\n" +
		"drwxrws--- 3 u0_a305 media_rw 3452 2026-07-28 19:09 แบบบ้าน/\n" +
		"-rwxrwx--- 1 u0_a305 media_rw 1234 2026-08-12 15:36 note.txt\n")})
	run.On("adb shell ls -lp -- /sdcard/Download/แบบบ้าน", fake.RunResult{Out: []byte("" +
		"total 4\n" +
		"-rwxrwx--- 1 u0_a305 media_rw 99 2026-08-12 15:36 plan.jpg\n")})
	run.On("adb shell ls -lp -- /sdcard/Download/ว่าง", fake.RunResult{Out: []byte("total 0\n")})
	svc := NewService(netList{{Name: "en0", IP: "192.168.1.23", Kind: domain.NetPhysical}}, 8080, "")
	svc.SetRunner(run)

	got, err := svc.PlanCableZip(context.Background(), owner(), nil, []string{"download"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("%+v", got)
	}
	if got[0].Loc != "download/แบบบ้าน/plan.jpg" || got[0].Name != "ดาวน์โหลด/แบบบ้าน/plan.jpg" {
		t.Fatalf("nested = %+v", got[0])
	}
	if got[1].Loc != "download/note.txt" || got[1].Name != "ดาวน์โหลด/note.txt" {
		t.Fatalf("file = %+v", got[1])
	}

	empty, err := svc.PlanCableZip(context.Background(), owner(), nil, []string{"download/ว่าง"})
	if err != nil {
		t.Fatal(err)
	}
	if len(empty) != 0 {
		t.Fatalf("empty folder = %+v", empty)
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
	got, err := svc.PeekCableFile(context.Background(), owner(), "download/note.txt", 0)
	if err != nil || got.Name != "note.txt" || string(got.Data) != "hello peek" {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := svc.PeekCableFile(context.Background(), domain.Device{Role: domain.RoleMember, RoomID: "main"}, "download/note.txt", 0); err != domain.ErrForbidden {
		t.Errorf("member peek = %v", err)
	}
	if _, err := svc.PeekCableFile(context.Background(), owner(), "download/../x", 0); err != domain.ErrInvalidInput {
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
	if _, err := svc.OpenCableFile(context.Background(), owner(), "download"); err != domain.ErrInvalidInput {
		t.Errorf("Open root file = %v", err)
	}
}

func TestParseDevicectlFiles(t *testing.T) {
	raw := []byte(`{"result":{"files":[{"name":"Scan.pdf","isDirectory":false,"size":9001},{"name":"Photos","isDirectory":true}]}}`)
	got, err := parseDevicectlFiles(raw, "documents", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "Scan.pdf" || got[0].Size != 9001 || got[0].Dir || !got[1].Dir {
		t.Fatalf("%+v", got)
	}
	if !strings.HasPrefix(got[0].Path, "documents/") {
		t.Fatalf("path = %s", got[0].Path)
	}
}
