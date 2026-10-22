package domain

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestSanitizeFileName(t *testing.T) {
	cases := map[string]string{
		"photo.jpg":            "photo.jpg",
		"../../etc/passwd":     "passwd",
		`C:\Users\me\file.txt`: "file.txt",
		"..":                   "file",
		"   ":                  "file",
		"a\x00b\nc.txt":        "abc.txt",
		".hidden.":             "hidden",
		"รายงานประชุม.docx":      "รายงานประชุม.docx",
		strings.Repeat("ก", 500): strings.Repeat("ก", 200),
	}
	for in, want := range cases {
		if got := SanitizeFileName(in); got != want {
			t.Errorf("SanitizeFileName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPIN(t *testing.T) {
	for _, ok := range []string{"000000", "482917"} {
		if !ValidPINFormat(ok) {
			t.Errorf("ValidPINFormat(%q) = false", ok)
		}
	}
	for _, bad := range []string{"", "12345", "1234567", "12a456", "１２３４５６"} {
		if ValidPINFormat(bad) {
			t.Errorf("ValidPINFormat(%q) = true", bad)
		}
	}
	// deterministic source: 6 zero bytes -> number 0 -> "000000"
	pin, err := NewPIN(bytes.NewReader(make([]byte, 64)))
	if err != nil || pin != "000000" {
		t.Fatalf("NewPIN = %q, %v; want 000000 (zero padded)", pin, err)
	}
	pin, err = NewPIN(nil)
	if err != nil || !ValidPINFormat(pin) {
		t.Fatalf("NewPIN(crypto) = %q, %v", pin, err)
	}
}

func TestRoomCheckPIN(t *testing.T) {
	r := Room{PIN: "482917"}
	if !r.CheckPIN("482917") || r.CheckPIN("482918") || r.CheckPIN("") || r.CheckPIN("4829170") {
		t.Fatal("CheckPIN gave a wrong answer")
	}
}

func TestRolePermissions(t *testing.T) {
	type perm struct{ up, down, manage bool }
	want := map[Role]perm{
		RoleOwner:    {true, true, true},
		RoleMember:   {true, true, false},
		RoleUploader: {true, false, false},
		RoleViewer:   {false, true, false},
		Role("x"):    {false, false, false},
	}
	for role, p := range want {
		got := perm{role.CanUpload(), role.CanDownload(), role.CanManage()}
		if got != p {
			t.Errorf("%q permissions = %+v, want %+v", role, got, p)
		}
	}
}

func TestNormalizeDeviceName(t *testing.T) {
	if got := NormalizeDeviceName("  \x07 "); got != defaultDeviceName {
		t.Errorf("blank name -> %q", got)
	}
	if got := NormalizeDeviceName(strings.Repeat("ก", 100)); len([]rune(got)) != maxDeviceNameRunes {
		t.Errorf("long name not truncated: %d runes", len([]rune(got)))
	}
}

func TestSanitizeDir(t *testing.T) {
	cases := map[string]string{
		"":                       "",
		"photos":                 "photos",
		"photos/2026":            "photos/2026",
		`photos\2026\trip`:       "photos/2026/trip",
		"../../etc":              "etc",
		"/abs/olute/":            "abs/olute",
		"a//b/./c":               "a/b/c",
		"..":                     "",
		" . / .. /x ":            "x",
		"a/\x00b":                "a/b",
		strings.Repeat("d/", 30): strings.TrimSuffix(strings.Repeat("d/", 12), "/"),
	}
	for in, want := range cases {
		if got := SanitizeDir(in); got != want {
			t.Errorf("SanitizeDir(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestUniqueName(t *testing.T) {
	taken := map[string]bool{"a.txt": true, "a (2).txt": true, ".env": true, "Makefile": true}
	has := func(s string) bool { return taken[s] }
	cases := map[string]string{
		"b.txt":    "b.txt",
		"a.txt":    "a (3).txt",
		".env":     ".env (2)",
		"Makefile": "Makefile (2)",
	}
	for in, want := range cases {
		if got := UniqueName(in, has); got != want {
			t.Errorf("UniqueName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestKindOfAndThumbnails(t *testing.T) {
	cases := map[string]FileKind{
		"a.JPG": KindImage, "b.mp4": KindVideo, "c.mp3": KindAudio, "d.pdf": KindDocument,
		"e.zip": KindArchive, "f": KindOther, "g.unknown": KindOther, "h.HEIC": KindImage,
	}
	for in, want := range cases {
		if got := KindOf(in); got != want {
			t.Errorf("KindOf(%q) = %q, want %q", in, got, want)
		}
	}
	for name, want := range map[string]bool{
		"a.jpg": true, "a.JPEG": true, "a.png": true, "a.gif": true, "a.webp": true,
		"a.heic": false, "a.svg": false, "a.mp4": false, "a": false,
	} {
		if got := CanThumbnail(name); got != want {
			t.Errorf("CanThumbnail(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestFilePath(t *testing.T) {
	if p := (File{Name: "x.txt"}).Path(); p != "x.txt" {
		t.Errorf("Path() = %q", p)
	}
	if p := (File{Dir: "a/b", Name: "x.txt"}).Path(); p != "a/b/x.txt" {
		t.Errorf("Path() = %q", p)
	}
}

func TestCleanText(t *testing.T) {
	if s, err := CleanText("  hi\r\nthere\x00\t!  ", 100, false); err != nil || s != "hi\nthere\t!" {
		t.Errorf("CleanText = %q, %v", s, err)
	}
	if _, err := CleanText("   ", 10, false); err == nil {
		t.Error("blank text accepted")
	}
	if s, err := CleanText("   ", 10, true); err != nil || s != "" {
		t.Errorf("blank text with allowEmpty = %q, %v", s, err)
	}
	if _, err := CleanText(strings.Repeat("ก", 11), 10, false); err == nil {
		t.Error("over-long text accepted")
	}
	if !(RoleOwner.CanChat() && RoleMember.CanChat()) || RoleViewer.CanChat() || RoleUploader.CanChat() {
		t.Error("CanChat roles are wrong")
	}
}

func TestValidRoomID(t *testing.T) {
	ok := []string{"main", "a", "team-b", "abc123", strings.Repeat("a", 32)}
	for _, s := range ok {
		if !ValidRoomID(s) {
			t.Errorf("%q should be valid", s)
		}
	}
	bad := []string{"", "Main", "team_b", "-x", "a/b", strings.Repeat("a", 33), "ทีม"}
	for _, s := range bad {
		if ValidRoomID(s) {
			t.Errorf("%q should be invalid", s)
		}
	}
}

func TestPendingDeviceCannotAct(t *testing.T) {
	d := Device{Role: RoleMember, Pending: true}
	if d.CanUpload() || d.CanDownload() || d.CanChat() || d.CanManage() {
		t.Fatal("pending member still has permissions")
	}
	d.Pending = false
	if !d.CanUpload() || !d.CanDownload() || !d.CanChat() {
		t.Fatal("approved member lost permissions")
	}
}

func TestGuestActive(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	r := Room{GuestKey: "aabbccddeeff0011", GuestUntil: now.Add(time.Hour)}
	if !r.GuestActive(now) {
		t.Fatal("active guest link reported inactive")
	}
	if r.GuestActive(now.Add(2 * time.Hour)) {
		t.Fatal("expired guest link reported active")
	}
	r.GuestKey = ""
	if r.GuestActive(now) {
		t.Fatal("empty key reported active")
	}
}
