package hostfs

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lanshare/internal/domain"
)

func TestBrowserListsAndOpensInjectedVolume(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	b := &Browser{detect: func() []volume {
		return []volume{{id: "download", label: "ดาวน์โหลด", abs: dir}}
	}}
	vols, err := b.Volumes(context.Background())
	if err != nil || len(vols) != 1 || vols[0].ID != "download" {
		t.Fatalf("%+v %v", vols, err)
	}
	ents, err := b.List(context.Background(), "download", "")
	if err != nil || len(ents) != 2 || !ents[0].Dir || ents[0].Name != "sub" || ents[1].Name != "note.txt" {
		t.Fatalf("%+v %v", ents, err)
	}
	f, size, err := b.Open(context.Background(), "download", "note.txt")
	if err != nil || size != 2 {
		t.Fatalf("open %v size=%d", err, size)
	}
	buf := make([]byte, 2)
	n, _ := f.Read(buf)
	_ = f.Close()
	if n != 2 || string(buf) != "hi" {
		t.Fatalf("read %q", buf)
	}
	if _, _, err := b.Open(context.Background(), "download", "../note.txt"); err != domain.ErrInvalidInput {
		t.Fatalf("escape = %v", err)
	}
}

func TestRemoveRefusesVolumeRoot(t *testing.T) {
	dir := t.TempDir()
	b := &Browser{detect: func() []volume {
		return []volume{{id: "all", label: "ที่เก็บข้อมูล", abs: dir}}
	}}
	if err := b.Remove(context.Background(), "all", ""); err != domain.ErrInvalidInput {
		t.Fatalf("remove root = %v", err)
	}
}

func TestPutWritesNestedFile(t *testing.T) {
	dir := t.TempDir()
	b := &Browser{detect: func() []volume {
		return []volume{{id: "download", label: "ดาวน์โหลด", abs: dir}}
	}}
	n, err := b.Put(context.Background(), "download", "sub/note.txt", strings.NewReader("hi"))
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "sub", "note.txt"))
	if err != nil || string(got) != "hi" {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := b.Put(context.Background(), "download", "", strings.NewReader("x")); err != domain.ErrInvalidInput {
		t.Fatalf("root put = %v", err)
	}
}

func TestExtraRootsFromJSON(t *testing.T) {
	data := t.TempDir()
	vol := t.TempDir()
	if err := os.WriteFile(filepath.Join(vol, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal([]extraRoot{{ID: "saf-usb", Label: "USB", Path: vol}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, extraFileName), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	b := New(data)
	vols, err := b.Volumes(context.Background())
	if err != nil || len(vols) != 1 || vols[0].ID != "saf-usb" || vols[0].Label != "USB" {
		t.Fatalf("%+v %v", vols, err)
	}
}
