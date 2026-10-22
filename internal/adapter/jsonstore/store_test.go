package jsonstore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lanshare/internal/domain"
)

func TestDevicesPersistWithoutOnline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	d := domain.Device{
		ID: "dev-1", RoomID: "main", Name: "Pixel", Platform: "Android",
		Role: domain.RoleMember, JoinedAt: time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC),
		Online: true, Pending: true,
	}
	if err := s.DeviceRepo().Save(ctx, d); err != nil {
		t.Fatal(err)
	}
	got, err := s.DeviceRepo().Get(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Online || !got.Pending {
		t.Fatalf("in-memory device = %+v", got)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := reopened.DeviceRepo().Get(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Online {
		t.Fatal("Online was persisted; a restart must load devices as offline")
	}
	if !loaded.Pending || loaded.Name != "Pixel" || loaded.Role != domain.RoleMember {
		t.Fatalf("reloaded device = %+v", loaded)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"online"`) {
		t.Fatalf("state.json should omit Online, got %s", raw)
	}
}

func TestDeleteRoomDropsFilesAndDevices(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	rm := domain.Room{ID: "gone", Name: "Gone", PIN: "123456", CreatedAt: time.Now()}
	if err := s.Save(ctx, rm); err != nil {
		t.Fatal(err)
	}
	if err := s.FileRepo().Save(ctx, domain.File{ID: "f1", RoomID: "gone", Name: "a.txt", Size: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeviceRepo().Save(ctx, domain.Device{ID: "d1", RoomID: "gone", Name: "Phone", Role: domain.RoleMember}); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, "gone"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, "gone"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("get deleted room: %v", err)
	}
	if _, err := s.FileRepo().Get(ctx, "gone", "f1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("file remained: %v", err)
	}
	if _, err := s.DeviceRepo().Get(ctx, "d1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("device remained: %v", err)
	}
}
