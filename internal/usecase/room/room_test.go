package room_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"lanshare/internal/domain"
	"lanshare/internal/usecase/fake"
	"lanshare/internal/usecase/room"
)

func TestCreateAndGuestLink(t *testing.T) {
	clock := fake.NewClock()
	svc := room.NewService(fake.NewRooms(domain.Room{ID: "main", Name: "Main", PIN: "482917", CreatedAt: clock.Now()}), clock, nil)
	ctx := context.Background()
	owner := domain.Device{ID: "o", RoomID: "main", Role: domain.RoleOwner}
	member := domain.Device{ID: "m", RoomID: "main", Role: domain.RoleMember}

	if _, err := svc.Create(ctx, member, "ทีม B"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("member create: %v", err)
	}
	rm, err := svc.Create(ctx, owner, "ทีม B")
	if err != nil {
		t.Fatal(err)
	}
	if rm.Name != "ทีม B" || rm.ID == "main" || !domain.ValidRoomID(string(rm.ID)) {
		t.Fatalf("created = %+v", rm)
	}
	if rm.RequireApproval {
		t.Fatalf("new room copied approval from main = %+v", rm)
	}

	needApprove := fake.NewRooms(domain.Room{ID: "main", Name: "Main", PIN: "482917", RequireApproval: true, CreatedAt: clock.Now()})
	gated, err := room.NewService(needApprove, clock, nil).Create(ctx, owner, "ทีม C")
	if err != nil {
		t.Fatal(err)
	}
	if !gated.RequireApproval {
		t.Fatalf("new room must keep RequireApproval of the current room: %+v", gated)
	}

	g, err := svc.RotateGuest(ctx, owner, time.Hour, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.GuestKey) != 16 || !g.GuestActive(clock.Now()) {
		t.Fatalf("guest = %+v", g)
	}
	found, err := svc.LookupGuest(ctx, g.GuestKey)
	if err != nil || found.ID != owner.RoomID {
		t.Fatalf("lookup: %+v %v", found, err)
	}
	if _, err := svc.LookupGuest(ctx, "ffffffffffffffff"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown key: %v", err)
	}
}

func TestDeleteRoomKeepsAtLeastOne(t *testing.T) {
	clock := fake.NewClock()
	repo := fake.NewRooms(domain.Room{ID: "main", Name: "Main", PIN: "482917", CreatedAt: clock.Now()})
	svc := room.NewService(repo, clock, nil)
	ctx := context.Background()
	owner := domain.Device{ID: "o", RoomID: "main", Role: domain.RoleOwner}

	if _, err := svc.Delete(ctx, owner, "main"); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("delete last: %v", err)
	}
	other, err := svc.Create(ctx, owner, "ทีม B")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Delete(ctx, owner, domain.DefaultRoomID); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("delete default while extras exist: %v", err)
	}
	if _, err := svc.Delete(ctx, domain.Device{ID: "m", RoomID: "main", Role: domain.RoleMember}, other.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("member delete: %v", err)
	}
	stay, err := svc.Delete(ctx, owner, other.ID)
	if err != nil || stay.ID != "main" {
		t.Fatalf("delete extra = %+v %v", stay, err)
	}
	if _, err := repo.Get(ctx, other.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("deleted room still present")
	}
}
