package transfer_test

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"lanshare/internal/domain"
	"lanshare/internal/usecase/fake"
	"lanshare/internal/usecase/port"
	"lanshare/internal/usecase/transfer"
)

type env struct {
	svc    *transfer.Service
	store  *fake.Store
	thumbs *fake.Thumbs
	render *fake.Renderer
	pub    *fake.Publisher
	clock  *fake.Clock
}

func newEnv() env {
	store := fake.NewStore()
	thumbs := fake.NewThumbs()
	render := &fake.Renderer{}
	pub := &fake.Publisher{}
	clock := fake.NewClock()
	svc := transfer.NewService(fake.NewFiles(), store, thumbs, render, pub, &fake.IDs{}, clock, 250*time.Millisecond)
	return env{svc, store, thumbs, render, pub, clock}
}

func (e env) upIn(actor domain.Device, in transfer.UploadInput) (domain.File, error) {
	return e.svc.Upload(context.Background(), actor, in)
}

func put(name, dir, content string, policy transfer.ConflictPolicy) transfer.UploadInput {
	return transfer.UploadInput{
		Name: name, Dir: dir, Size: int64(len(content)), Conflict: policy,
		Body: bytes.NewReader([]byte(content)),
	}
}

func dev(id string, room domain.RoomID, role domain.Role) domain.Device {
	return domain.Device{ID: domain.DeviceID(id), RoomID: room, Name: id, Role: role}
}

func TestUploadListDownloadRoundTrip(t *testing.T) {
	e := newEnv()
	ctx := context.Background()
	alice := dev("alice", "A", domain.RoleMember)

	f, err := e.upIn(alice, put("../notes.txt", "", "hello", ""))
	if err != nil {
		t.Fatal(err)
	}
	if f.Name != "notes.txt" || f.Size != 5 || f.RoomID != "A" {
		t.Fatalf("unexpected file %+v", f)
	}
	list, err := e.svc.List(ctx, alice)
	if err != nil || len(list) != 1 {
		t.Fatalf("List = %v, %v", list, err)
	}
	dl, err := e.svc.Open(ctx, alice, f.ID)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(dl.Content)
	dl.Content.Close()
	if string(got) != "hello" {
		t.Fatalf("content = %q", got)
	}
	if e.pub.Count(port.EventFileAdded) != 1 {
		t.Fatal("expected file.added event")
	}
}

// The core requirement for teams: a room can never reach another room's files.
func TestRoomsAreIsolated(t *testing.T) {
	e := newEnv()
	ctx := context.Background()
	inA := dev("a", "A", domain.RoleMember)
	inB := dev("b", "B", domain.RoleMember)
	ownerB := dev("ownerB", "B", domain.RoleOwner)

	f, err := e.upIn(inA, put("secret.txt", "", "abc", ""))
	if err != nil {
		t.Fatal(err)
	}

	list, _ := e.svc.List(ctx, inB)
	if len(list) != 0 {
		t.Fatalf("room B sees room A files: %v", list)
	}
	if _, err := e.svc.Open(ctx, inB, f.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("room B opened room A file: err = %v", err)
	}
	if err := e.svc.Delete(ctx, ownerB, f.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("room B owner deleted room A file: err = %v", err)
	}
	if _, err := e.svc.Thumbnail(ctx, inB, f.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("room B got a thumbnail of room A file: err = %v", err)
	}
	if _, err := e.svc.PlanArchive(ctx, inB, []domain.FileID{f.ID}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("room B zipped room A file: err = %v", err)
	}
	if !e.store.Has("A", f.ID) {
		t.Fatal("room A file content was removed by room B")
	}
}

func TestRolesAreEnforced(t *testing.T) {
	e := newEnv()
	ctx := context.Background()
	owner := dev("o", "A", domain.RoleOwner)
	viewer := dev("v", "A", domain.RoleViewer)
	uploader := dev("u", "A", domain.RoleUploader)
	member := dev("m", "A", domain.RoleMember)

	f, err := e.upIn(owner, put("x.bin", "", "1", ""))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := e.upIn(viewer, put("y", "", "1", "")); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("viewer upload: %v", err)
	}
	if _, err := e.svc.List(ctx, uploader); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("uploader list: %v", err)
	}
	if _, err := e.svc.Open(ctx, uploader, f.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("uploader download: %v", err)
	}
	if _, err := e.svc.PlanArchive(ctx, uploader, []domain.FileID{f.ID}); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("uploader zip: %v", err)
	}
	if _, err := e.svc.Thumbnail(ctx, uploader, f.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("uploader thumbnail: %v", err)
	}
	if _, err := e.upIn(uploader, put("z", "", "1", "")); err != nil {
		t.Errorf("uploader upload should work: %v", err)
	}
	if err := e.svc.Delete(ctx, member, f.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("member delete: %v", err)
	}
	if err := e.svc.Delete(ctx, owner, f.ID); err != nil {
		t.Errorf("owner delete: %v", err)
	}
	if e.store.Has("A", f.ID) {
		t.Error("delete left the content behind")
	}

	pending := domain.Device{ID: "p", RoomID: "A", Name: "p", Role: domain.RoleMember, Pending: true}
	if _, err := e.upIn(pending, put("nope.bin", "", "1", "")); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("pending upload: %v", err)
	}
}

type failingReader struct{ n int }

func (r *failingReader) Read(p []byte) (int, error) {
	if r.n > 0 {
		k := copy(p, bytes.Repeat([]byte("x"), min(len(p), r.n)))
		r.n -= k
		return k, nil
	}
	return 0, errors.New("connection reset")
}

func TestRoomQuotaBlocksUpload(t *testing.T) {
	e := newEnv()
	e.svc.SetRooms(fake.NewRooms(domain.Room{ID: "A", QuotaBytes: 4}))
	alice := dev("alice", "A", domain.RoleMember)
	if _, err := e.upIn(alice, put("a.bin", "", "12345", "")); err == nil {
		t.Fatal("upload over quota succeeded")
	} else {
		var space *domain.InsufficientStorageError
		if !errors.As(err, &space) {
			t.Fatalf("want InsufficientStorageError, got %v", err)
		}
	}
}

func TestFailedUploadLeavesNoFile(t *testing.T) {
	e := newEnv()
	ctx := context.Background()
	m := dev("m", "A", domain.RoleMember)

	_, err := e.upIn(m, transfer.UploadInput{Name: "big.bin", Size: 100, Body: &failingReader{n: 10}})
	if err == nil {
		t.Fatal("expected an error")
	}
	if list, _ := e.svc.List(ctx, m); len(list) != 0 {
		t.Fatalf("failed upload was listed: %v", list)
	}
	last := e.pub.Events[len(e.pub.Events)-1]
	p, ok := last.Data.(transfer.Progress)
	if last.Type != port.EventTransfer || !ok || p.State != transfer.StateFailed {
		t.Fatalf("last event = %+v, want a failed transfer", last)
	}
}

func TestProgressIsThrottledAndReportsSpeed(t *testing.T) {
	e := newEnv()
	m := dev("m", "A", domain.RoleMember)

	// a reader that advances the fake clock by 1s per 1 MiB chunk
	r := &clockedReader{clock: e.clock, chunks: 4, size: 1 << 20}
	if _, err := e.upIn(m, transfer.UploadInput{Name: "v.mp4", Size: 4 << 20, Body: r}); err != nil {
		t.Fatal(err)
	}
	var running []transfer.Progress
	var final transfer.Progress
	for _, ev := range e.pub.Events {
		if ev.Type != port.EventTransfer {
			continue
		}
		p := ev.Data.(transfer.Progress)
		if p.State == transfer.StateRunning {
			running = append(running, p)
		} else {
			final = p
		}
	}
	if len(running) != 4 {
		t.Fatalf("got %d running events, want 4 (one per second)", len(running))
	}
	if running[0].Speed != 1<<20 {
		t.Errorf("speed = %d, want %d", running[0].Speed, 1<<20)
	}
	if final.State != transfer.StateDone || final.Bytes != 4<<20 || final.Total != 4<<20 {
		t.Errorf("final = %+v", final)
	}

	// a burst of tiny reads inside the interval produces no extra events
	before := len(e.pub.Events)
	if _, err := e.upIn(m, put("tiny", "", "abc", "")); err != nil {
		t.Fatal(err)
	}
	// only done + file.added
	if got := len(e.pub.Events) - before; got != 2 {
		t.Errorf("tiny upload published %d events, want 2", got)
	}
}

type clockedReader struct {
	clock  *fake.Clock
	chunks int
	size   int
	read   int
}

func (r *clockedReader) Read(p []byte) (int, error) {
	if r.read >= r.chunks {
		return 0, io.EOF
	}
	r.clock.Advance(time.Second)
	n := min(len(p), r.size)
	r.read++
	return n, nil
}

func TestDownloadReportsDoneOnlyWhenFullyRead(t *testing.T) {
	e := newEnv()
	ctx := context.Background()
	m := dev("m", "A", domain.RoleMember)
	f, _ := e.upIn(m, put("d.bin", "", "zzzzzzzzzz", ""))

	lastState := func() string {
		for i := len(e.pub.Events) - 1; i >= 0; i-- {
			if p, ok := e.pub.Events[i].Data.(transfer.Progress); ok && p.Direction == transfer.DirectionDownload {
				return p.State
			}
		}
		return ""
	}

	dl, _ := e.svc.Open(ctx, m, f.ID)
	_, _ = dl.Content.Read(make([]byte, 4))
	dl.Content.Close()
	if lastState() != transfer.StateStopped {
		t.Errorf("partial download state = %q, want stopped", lastState())
	}

	dl, _ = e.svc.Open(ctx, m, f.ID)
	_, _ = io.ReadAll(dl.Content)
	dl.Content.Close()
	if lastState() != transfer.StateDone {
		t.Errorf("full download state = %q, want done", lastState())
	}
}

// ---------------- Phase B ----------------

func TestSameNameIsRenamedByDefault(t *testing.T) {
	e := newEnv()
	m := dev("m", "A", domain.RoleMember)

	var names []string
	for i := 0; i < 3; i++ {
		f, err := e.upIn(m, put("report.pdf", "", "x", ""))
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, f.Name)
	}
	want := []string{"report.pdf", "report (2).pdf", "report (3).pdf"}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("names = %v, want %v", names, want)
		}
	}
	// case-insensitive, and a different folder is a different file
	if f, _ := e.upIn(m, put("REPORT.PDF", "", "x", "")); f.Name != "REPORT (4).PDF" {
		t.Errorf("case-insensitive rename = %q", f.Name)
	}
	if f, _ := e.upIn(m, put("report.pdf", "other", "x", "")); f.Name != "report.pdf" {
		t.Errorf("same name in another folder = %q, want unchanged", f.Name)
	}
}

func TestOverwriteReplacesAndCleansUp(t *testing.T) {
	e := newEnv()
	ctx := context.Background()
	m := dev("m", "A", domain.RoleMember)

	old, _ := e.upIn(m, put("a.png", "", "old", ""))
	e.thumbs.PutThumb(ctx, "A", old.ID, []byte("cached"))

	f, err := e.upIn(m, put("a.png", "", "newer", transfer.ConflictOverwrite))
	if err != nil {
		t.Fatal(err)
	}
	if f.Name != "a.png" || f.ID == old.ID {
		t.Fatalf("overwrite result %+v (old %v)", f, old.ID)
	}
	list, _ := e.svc.List(ctx, m)
	if len(list) != 1 || list[0].ID != f.ID {
		t.Fatalf("list after overwrite = %v", list)
	}
	if e.store.Has("A", old.ID) || e.thumbs.Has("A", old.ID) {
		t.Error("old content or thumbnail left behind")
	}
	if e.pub.Count(port.EventFileDeleted) != 1 {
		t.Error("expected a file.deleted event for the replaced file")
	}
}

func TestOverwriteOnlyByOwnerOrUploader(t *testing.T) {
	e := newEnv()
	alice := dev("alice", "A", domain.RoleMember)
	bob := dev("bob", "A", domain.RoleMember)
	owner := dev("owner", "A", domain.RoleOwner)

	orig, _ := e.upIn(alice, put("plan.docx", "", "v1", ""))

	if _, err := e.upIn(bob, put("plan.docx", "", "evil", transfer.ConflictOverwrite)); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("bob overwrote alice's file: err = %v", err)
	}
	if !e.store.Has("A", orig.ID) {
		t.Fatal("alice's file was damaged by a refused overwrite")
	}
	if _, err := e.upIn(alice, put("plan.docx", "", "v2", transfer.ConflictOverwrite)); err != nil {
		t.Fatalf("uploader should overwrite own file: %v", err)
	}
	if _, err := e.upIn(owner, put("plan.docx", "", "v3", transfer.ConflictOverwrite)); err != nil {
		t.Fatalf("owner should overwrite: %v", err)
	}
	// overwriting a name that does not exist is a plain upload
	if f, err := e.upIn(bob, put("fresh.txt", "", "x", transfer.ConflictOverwrite)); err != nil || f.Name != "fresh.txt" {
		t.Fatalf("overwrite of missing name: %v %v", f, err)
	}
}

func TestFolderUploadKeepsSanitizedDir(t *testing.T) {
	e := newEnv()
	m := dev("m", "A", domain.RoleMember)
	f, err := e.upIn(m, put("img.jpg", `../../etc/./photos\2026//`, "x", ""))
	if err != nil {
		t.Fatal(err)
	}
	if f.Dir != "etc/photos/2026" || f.Path() != "etc/photos/2026/img.jpg" {
		t.Fatalf("dir = %q path = %q", f.Dir, f.Path())
	}
}

func TestArchiveKeepsFolderStructure(t *testing.T) {
	e := newEnv()
	ctx := context.Background()
	m := dev("m", "A", domain.RoleMember)

	a, _ := e.upIn(m, put("a.txt", "", "AAA", ""))
	b, _ := e.upIn(m, put("b.txt", "docs/sub", "BBB", ""))
	c, _ := e.upIn(m, put("a.txt", "", "CCC", "")) // stored as "a (2).txt"

	plan, err := e.svc.PlanArchive(ctx, m, []domain.FileID{a.ID, b.ID, c.ID})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := plan.WriteTo(ctx, &buf); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("not a valid zip: %v", err)
	}
	got := map[string]string{}
	for _, zf := range zr.File {
		rc, _ := zf.Open()
		data, _ := io.ReadAll(rc)
		rc.Close()
		got[zf.Name] = string(data)
	}
	want := map[string]string{"a.txt": "AAA", "docs/sub/b.txt": "BBB", "a (2).txt": "CCC"}
	if len(got) != len(want) {
		t.Fatalf("zip entries = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("entry %q = %q, want %q (all: %v)", k, got[k], v, got)
		}
	}
	if plan.TotalSize() != 9 {
		t.Errorf("TotalSize = %d", plan.TotalSize())
	}
	last := e.pub.Events[len(e.pub.Events)-1]
	if p, ok := last.Data.(transfer.Progress); !ok || p.State != transfer.StateDone || p.Bytes != 9 {
		t.Errorf("last event = %+v", last)
	}
}

func TestArchiveValidation(t *testing.T) {
	e := newEnv()
	ctx := context.Background()
	m := dev("m", "A", domain.RoleMember)
	if _, err := e.svc.PlanArchive(ctx, m, nil); !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("empty selection: %v", err)
	}
	if _, err := e.svc.PlanArchive(ctx, m, []domain.FileID{"nope"}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown file: %v", err)
	}
	many := make([]domain.FileID, transfer.MaxArchiveFiles+1)
	for i := range many {
		many[i] = "x"
	}
	if _, err := e.svc.PlanArchive(ctx, m, many); !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("too many files: %v", err)
	}
}

func TestThumbnailIsRenderedOnceAndCached(t *testing.T) {
	e := newEnv()
	ctx := context.Background()
	m := dev("m", "A", domain.RoleMember)
	img, _ := e.upIn(m, put("p.jpg", "", "PIXELS", ""))
	txt, _ := e.upIn(m, put("n.txt", "", "text", ""))

	for i := 0; i < 3; i++ {
		b, err := e.svc.Thumbnail(ctx, m, img.ID)
		if err != nil || string(b) != "thumb:PIXELS" {
			t.Fatalf("Thumbnail = %q, %v", b, err)
		}
	}
	if e.render.Calls != 1 {
		t.Errorf("render called %d times, want 1 (cached)", e.render.Calls)
	}
	if _, err := e.svc.Thumbnail(ctx, m, txt.ID); !errors.Is(err, domain.ErrUnsupported) {
		t.Errorf("text file thumbnail: %v", err)
	}

	e.render.Err = domain.ErrUnsupported
	bad, _ := e.upIn(m, put("broken.png", "", "not an image", ""))
	if _, err := e.svc.Thumbnail(ctx, m, bad.ID); !errors.Is(err, domain.ErrUnsupported) {
		t.Errorf("corrupt image: %v", err)
	}
	if e.thumbs.Has("A", bad.ID) {
		t.Error("a failed render must not be cached")
	}
}
