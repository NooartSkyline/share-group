package auth_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"lanshare/internal/adapter/memory"
	"lanshare/internal/domain"
	"lanshare/internal/usecase/auth"
	"lanshare/internal/usecase/fake"
	"lanshare/internal/usecase/port"
)

type env struct {
	svc   *auth.Service
	clock *fake.Clock
	pub   *fake.Publisher
}

func newEnv(t *testing.T) env {
	t.Helper()
	clock := fake.NewClock()
	pub := &fake.Publisher{}
	rooms := fake.NewRooms(domain.Room{ID: "main", Name: "Main", PIN: "482917"})
	svc := auth.NewService(
		rooms,
		memory.NewDeviceRepository(),
		memory.NewTokenService(clock, time.Hour),
		memory.NewAttemptLimiter(clock, memory.LimiterPolicy{MaxFailures: 5, Window: 10 * time.Minute, Lockout: 5 * time.Minute}),
		&fake.IDs{}, clock, pub,
	)
	return env{svc, clock, pub}
}

func join(e env, pin string) (auth.Session, error) {
	return e.svc.Join(context.Background(), auth.JoinInput{
		RoomID: "main", PIN: pin, DeviceName: "Pixel", Platform: "Android",
		RemoteAddr: "10.0.0.5", LimiterKey: "10.0.0.5|main",
	})
}

func TestJoinWithCorrectPIN(t *testing.T) {
	e := newEnv(t)
	s, err := join(e, "482917")
	if err != nil {
		t.Fatal(err)
	}
	if s.Device.Role != domain.RoleMember || s.Token == "" {
		t.Fatalf("unexpected session %+v", s)
	}
	got, err := e.svc.Authenticate(context.Background(), s.Token)
	if err != nil || got.ID != s.Device.ID {
		t.Fatalf("Authenticate = %+v, %v", got, err)
	}
	if e.pub.Count(port.EventDevices) != 1 {
		t.Fatal("joining should announce the device list changed")
	}
}

func TestWrongPINCountsDownThenLocks(t *testing.T) {
	e := newEnv(t)
	for want := 4; want >= 1; want-- {
		_, err := join(e, "000000")
		var inv *domain.InvalidPINError
		if !errors.As(err, &inv) || inv.Remaining != want {
			t.Fatalf("attempt: err = %v, want InvalidPINError with %d remaining", err, want)
		}
	}
	// fifth failure locks
	_, err := join(e, "000000")
	var locked *domain.LockedError
	if !errors.As(err, &locked) || locked.RetryAfter != 5*time.Minute {
		t.Fatalf("fifth failure: err = %v, want 5 minute lockout", err)
	}
	// even the right PIN is refused while locked
	if _, err := join(e, "482917"); !errors.Is(err, domain.ErrLocked) {
		t.Fatalf("correct PIN while locked: err = %v, want ErrLocked", err)
	}
	// lockout expires
	e.clock.Advance(5*time.Minute + time.Second)
	if _, err := join(e, "482917"); err != nil {
		t.Fatalf("after lockout expired: %v", err)
	}
}

func TestSuccessResetsFailureCount(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 3; i++ {
		_, _ = join(e, "111111")
	}
	if _, err := join(e, "482917"); err != nil {
		t.Fatal(err)
	}
	_, err := join(e, "111111")
	var inv *domain.InvalidPINError
	if !errors.As(err, &inv) || inv.Remaining != 4 {
		t.Fatalf("counter should have reset, got %v", err)
	}
}

func TestMalformedPINCountsAsFailure(t *testing.T) {
	e := newEnv(t)
	_, err := join(e, "abc")
	if !errors.Is(err, domain.ErrInvalidPIN) {
		t.Fatalf("err = %v", err)
	}
}

func TestLockoutIsPerClient(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 5; i++ {
		_, _ = join(e, "000000")
	}
	_, err := e.svc.Join(context.Background(), auth.JoinInput{
		RoomID: "main", PIN: "482917", DeviceName: "iPhone", LimiterKey: "10.0.0.9|main",
	})
	if err != nil {
		t.Fatalf("another client must not be locked: %v", err)
	}
}

func TestUnknownRoom(t *testing.T) {
	e := newEnv(t)
	_, err := e.svc.Join(context.Background(), auth.JoinInput{RoomID: "nope", PIN: "482917", LimiterKey: "k"})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestOwnerSessionIsStableAndOwner(t *testing.T) {
	e := newEnv(t)
	a, err := e.svc.OwnerSession(context.Background(), "main", "Mac")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := e.svc.OwnerSession(context.Background(), "main", "Mac")
	if a.Device.ID != b.Device.ID || a.Device.Role != domain.RoleOwner || a.Token == b.Token {
		t.Fatalf("owner session: %+v vs %+v", a, b)
	}
}

func TestAuthenticateRejectsGarbage(t *testing.T) {
	e := newEnv(t)
	for _, tok := range []string{"", "nope"} {
		if _, err := e.svc.Authenticate(context.Background(), tok); !errors.Is(err, domain.ErrUnauthorized) {
			t.Errorf("token %q: err = %v", tok, err)
		}
	}
}

func TestTokenExpires(t *testing.T) {
	e := newEnv(t)
	s, _ := join(e, "482917")
	e.clock.Advance(2 * time.Hour)
	if _, err := e.svc.Authenticate(context.Background(), s.Token); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("expired token accepted: %v", err)
	}
}
