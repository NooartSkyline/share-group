package chat_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"lanshare/internal/adapter/memory"
	"lanshare/internal/domain"
	"lanshare/internal/usecase/chat"
	"lanshare/internal/usecase/fake"
	"lanshare/internal/usecase/port"
)

func newSvc() (*chat.Service, *fake.Publisher) {
	pub := &fake.Publisher{}
	return chat.NewService(memory.NewChatRepository(), pub, &fake.IDs{}, fake.NewClock()), pub
}

func dev(id string, room domain.RoomID, role domain.Role) domain.Device {
	return domain.Device{ID: domain.DeviceID(id), RoomID: room, Name: id, Role: role}
}

func TestSendAndRecentAreRoomScoped(t *testing.T) {
	svc, pub := newSvc()
	ctx := context.Background()
	a1 := dev("a1", "A", domain.RoleMember)
	a2 := dev("a2", "A", domain.RoleOwner)
	b1 := dev("b1", "B", domain.RoleMember)

	for _, txt := range []string{"first", "  second \n"} {
		if _, err := svc.Send(ctx, a1, txt); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.Send(ctx, b1, "other room"); err != nil {
		t.Fatal(err)
	}

	got, err := svc.Recent(ctx, a2)
	if err != nil || len(got) != 2 || got[0].Text != "first" || got[1].Text != "second" {
		t.Fatalf("Recent(A) = %+v, %v (oldest first, trimmed)", got, err)
	}
	if got[0].DeviceName != "a1" {
		t.Errorf("sender name = %q", got[0].DeviceName)
	}
	if other, _ := svc.Recent(ctx, b1); len(other) != 1 || other[0].Text != "other room" {
		t.Fatalf("Recent(B) leaked or lost messages: %+v", other)
	}
	if pub.Count(port.EventMessage) != 3 {
		t.Errorf("message events = %d, want 3", pub.Count(port.EventMessage))
	}
	for _, e := range pub.Events {
		if e.Type == port.EventMessage && e.RoomID == "" {
			t.Error("event without a room would be broadcast nowhere")
		}
	}
}

func TestMessageValidation(t *testing.T) {
	svc, _ := newSvc()
	ctx := context.Background()
	m := dev("m", "A", domain.RoleMember)

	if _, err := svc.Send(ctx, m, "  \n\t "); !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("blank message: %v", err)
	}
	if _, err := svc.Send(ctx, m, strings.Repeat("ก", domain.MaxMessageRunes+1)); !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("too long message: %v", err)
	}
	if v, err := svc.Send(ctx, m, strings.Repeat("ก", domain.MaxMessageRunes)); err != nil || len([]rune(v.Text)) != domain.MaxMessageRunes {
		t.Errorf("message at the limit: %v", err)
	}
	v, err := svc.Send(ctx, m, "a\x00b\x07c\r\nd")
	if err != nil || v.Text != "abc\nd" {
		t.Errorf("control characters: %q, %v", v.Text, err)
	}
}

func TestRolesForChat(t *testing.T) {
	svc, _ := newSvc()
	ctx := context.Background()
	viewer := dev("v", "A", domain.RoleViewer)
	uploader := dev("u", "A", domain.RoleUploader)

	if _, err := svc.Send(ctx, viewer, "hi"); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("viewer send: %v", err)
	}
	if _, err := svc.SetClipboard(ctx, viewer, "x"); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("viewer set clipboard: %v", err)
	}
	if _, err := svc.Recent(ctx, viewer); err != nil {
		t.Errorf("viewer should read messages: %v", err)
	}
	if _, err := svc.Recent(ctx, uploader); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("upload-only guest read messages: %v", err)
	}
	if _, err := svc.Clipboard(ctx, uploader); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("upload-only guest read clipboard: %v", err)
	}
}

func TestClipboard(t *testing.T) {
	svc, pub := newSvc()
	ctx := context.Background()
	a := dev("a", "A", domain.RoleMember)
	b := dev("b", "B", domain.RoleMember)

	if c, err := svc.Clipboard(ctx, a); err != nil || c.Text != "" {
		t.Fatalf("empty clipboard: %+v %v", c, err)
	}
	if _, err := svc.SetClipboard(ctx, a, "https://example.com/x"); err != nil {
		t.Fatal(err)
	}
	if c, _ := svc.Clipboard(ctx, a); c.Text != "https://example.com/x" || c.DeviceName != "a" {
		t.Fatalf("clipboard = %+v", c)
	}
	if c, _ := svc.Clipboard(ctx, b); c.Text != "" {
		t.Fatalf("clipboard leaked across rooms: %+v", c)
	}
	// empty text clears it
	if _, err := svc.SetClipboard(ctx, a, ""); err != nil {
		t.Fatalf("clearing: %v", err)
	}
	if c, _ := svc.Clipboard(ctx, a); c.Text != "" {
		t.Fatalf("not cleared: %+v", c)
	}
	if _, err := svc.SetClipboard(ctx, a, strings.Repeat("x", domain.MaxClipboardRunes+1)); !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("oversized clipboard: %v", err)
	}
	if pub.Count(port.EventClipboard) != 2 {
		t.Errorf("clipboard events = %d, want 2", pub.Count(port.EventClipboard))
	}
}

func TestOnlyLatestMessagesAreKept(t *testing.T) {
	svc, _ := newSvc()
	ctx := context.Background()
	m := dev("m", "A", domain.RoleMember)
	for i := 0; i < 250; i++ {
		if _, err := svc.Send(ctx, m, "msg "+strings.Repeat("x", i%7)+string(rune('A'+i%26))); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := svc.Recent(ctx, m)
	if len(got) != 100 {
		t.Fatalf("Recent returned %d messages, want 100", len(got))
	}
}
