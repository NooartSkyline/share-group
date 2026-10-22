package httpapi_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"image"
	"image/jpeg"
	"io"
	iofs "io/fs"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"lanshare/internal/adapter/filestore"
	"lanshare/internal/adapter/httpapi"
	"lanshare/internal/adapter/jsonstore"
	"lanshare/internal/adapter/memory"
	"lanshare/internal/adapter/qr"
	"lanshare/internal/adapter/thumb"
	"lanshare/internal/adapter/ws"
	"lanshare/internal/domain"
	"lanshare/internal/infra/system"
	"lanshare/internal/usecase/auth"
	"lanshare/internal/usecase/chat"
	"lanshare/internal/usecase/device"
	"lanshare/internal/usecase/network"
	"lanshare/internal/usecase/room"
	"lanshare/internal/usecase/transfer"
	"lanshare/web"
)

type fakeNet struct{}

func (fakeNet) Interfaces(context.Context) ([]domain.NetInterface, error) {
	return []domain.NetInterface{
		{Name: "utun3", IP: "100.64.0.2", Kind: domain.NetVPN},
		{Name: "en0", IP: "192.168.1.23", Kind: domain.NetPhysical},
	}, nil
}

// newServer wires the real stack on a temp dir and returns the test server and the room PIN.
func newServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	dir := t.TempDir()
	clock := system.Clock{}
	ids := system.IDGenerator{}
	store, err := jsonstore.Open(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	devices := memory.NewDeviceRepository()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	hub := ws.NewHub(nil, log)

	rooms := room.NewService(store.RoomRepo(), clock, rand.Reader)
	authSvc := auth.NewService(store.RoomRepo(), devices, memory.NewTokenService(clock, time.Hour),
		memory.NewAttemptLimiter(clock, memory.LimiterPolicy{MaxFailures: 3, Window: time.Minute, Lockout: time.Minute}),
		ids, clock, hub)
	deviceSvc := device.NewService(devices, hub)
	hub.SetPresence(deviceSvc)

	def, err := rooms.EnsureDefault(context.Background(), "main", "ห้องหลัก")
	if err != nil {
		t.Fatal(err)
	}
	webFS, _ := iofs.Sub(web.Files, ".")
	fs := filestore.New(dir)
	xfer := transfer.NewService(store.FileRepo(), fs, fs, thumb.New(), hub, ids, clock, 10*time.Millisecond)
	xfer.SetRooms(store.RoomRepo())
	srv := httpapi.NewServer(httpapi.Deps{
		Auth: authSvc, Rooms: rooms, Devices: deviceSvc,
		Transfer:    xfer,
		Chat:        chat.NewService(memory.NewChatRepository(), hub, ids, clock),
		Network:     network.NewService(fakeNet{}, 8080, ""),
		QR:          qr.New(),
		Hub:         hub,
		Web:         webFS,
		DefaultRoom: def.ID,
		Log:         log,
		Clock:       clock,
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, def.PIN
}

func newClient(t *testing.T) *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar}
}

func do(t *testing.T, c *http.Client, method, url string, body io.Reader, hdr map[string]string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-LanShare-Client", "1")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res, b
}

func join(t *testing.T, c *http.Client, ts *httptest.Server, pin string) (*http.Response, map[string]any) {
	return joinNamed(t, c, ts, pin, "Pixel 8", "Android")
}

func joinNamed(t *testing.T, c *http.Client, ts *httptest.Server, pin, name, platform string) (*http.Response, map[string]any) {
	t.Helper()
	body := `{"pin":"` + pin + `","deviceName":"` + name + `","platform":"` + platform + `"}`
	res, b := do(t, c, "POST", ts.URL+"/api/rooms/main/join", strings.NewReader(body), map[string]string{"Content-Type": "application/json"})
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return res, m
}

func TestUnauthenticatedRequestsAreRejected(t *testing.T) {
	ts, _ := newServer(t)
	c := newClient(t)
	for _, p := range []string{"/api/me", "/api/files", "/api/devices", "/api/admin/join-info", "/ws"} {
		res, _ := do(t, c, "GET", ts.URL+p, nil, nil)
		if res.StatusCode != http.StatusUnauthorized {
			t.Errorf("GET %s = %d, want 401", p, res.StatusCode)
		}
	}
}

func TestJoinUploadListDownload(t *testing.T) {
	ts, pin := newServer(t)
	c := newClient(t)

	if res, _ := join(t, c, ts, pin); res.StatusCode != 200 {
		t.Fatalf("join = %d", res.StatusCode)
	}

	payload := bytes.Repeat([]byte("0123456789"), 100_000) // 1 MB
	res, body := do(t, c, "POST", ts.URL+"/api/files?name="+"%E0%B8%A3%E0%B8%B2%E0%B8%A2%E0%B8%87%E0%B8%B2%E0%B8%99.bin",
		bytes.NewReader(payload), map[string]string{"Content-Type": "application/octet-stream"})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("upload = %d %s", res.StatusCode, body)
	}
	var f struct {
		ID, Name string
		Size     int64
	}
	_ = json.Unmarshal(body, &f)
	if f.Name != "รายงาน.bin" || f.Size != int64(len(payload)) {
		t.Fatalf("uploaded file = %+v", f)
	}

	_, listBody := do(t, c, "GET", ts.URL+"/api/files", nil, nil)
	var list []map[string]any
	_ = json.Unmarshal(listBody, &list)
	if len(list) != 1 {
		t.Fatalf("list = %s", listBody)
	}

	res, got := do(t, c, "GET", ts.URL+"/api/files/"+f.ID, nil, nil)
	if res.StatusCode != 200 || !bytes.Equal(got, payload) {
		t.Fatalf("download = %d, %d bytes", res.StatusCode, len(got))
	}
	if cd := res.Header.Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment") {
		t.Errorf("Content-Disposition = %q, uploads must never be served inline", cd)
	}

	// range request (resume / seeking)
	res, got = do(t, c, "GET", ts.URL+"/api/files/"+f.ID, nil, map[string]string{"Range": "bytes=10-19"})
	if res.StatusCode != http.StatusPartialContent || string(got) != "0123456789" {
		t.Errorf("range = %d %q", res.StatusCode, got)
	}
}

func TestWrongPINLocksOutAndIssuesNoSession(t *testing.T) {
	ts, pin := newServer(t)
	c := newClient(t)
	wrong := "000000"
	if pin == wrong {
		wrong = "111111"
	}
	for i := 0; i < 2; i++ {
		res, m := join(t, c, ts, wrong)
		if res.StatusCode != 401 || m["error"] != "invalid_pin" {
			t.Fatalf("attempt %d = %d %v", i, res.StatusCode, m)
		}
	}
	res, m := join(t, c, ts, wrong)
	if res.StatusCode != 429 || m["error"] != "locked" || res.Header.Get("Retry-After") == "" {
		t.Fatalf("third failure = %d %v", res.StatusCode, m)
	}
	// correct PIN is refused while locked and no cookie is set
	res, _ = join(t, c, ts, pin)
	if res.StatusCode != 429 {
		t.Fatalf("correct PIN while locked = %d", res.StatusCode)
	}
	if res, _ := do(t, c, "GET", ts.URL+"/api/me", nil, nil); res.StatusCode != 401 {
		t.Fatalf("locked client got a session: %d", res.StatusCode)
	}
}

func TestMemberCannotUseOwnerEndpoints(t *testing.T) {
	ts, pin := newServer(t)
	c := newClient(t)
	join(t, c, ts, pin)
	for _, p := range []string{"/api/admin/join-info", "/api/admin/network", "/api/admin/qr.png"} {
		if res, _ := do(t, c, "GET", ts.URL+p, nil, nil); res.StatusCode != http.StatusForbidden {
			t.Errorf("member GET %s = %d, want 403", p, res.StatusCode)
		}
	}
	if res, _ := do(t, c, "POST", ts.URL+"/api/admin/pin/rotate", nil, nil); res.StatusCode != http.StatusForbidden {
		t.Errorf("member rotate PIN = %d, want 403", res.StatusCode)
	}
}

func TestOwnerSessionOnlyFromThisMac(t *testing.T) {
	ts, _ := newServer(t)
	c := newClient(t)

	// the test server listens on 127.0.0.1, so a localhost request is accepted
	res, _ := do(t, c, "POST", ts.URL+"/api/owner/session", nil, nil)
	if res.StatusCode != 200 {
		t.Fatalf("owner session from localhost = %d", res.StatusCode)
	}
	_, b := do(t, c, "GET", ts.URL+"/api/admin/join-info", nil, nil)
	var info struct {
		PIN string
		URL string
	}
	_ = json.Unmarshal(b, &info)
	if len(info.PIN) != 6 || info.URL != "http://192.168.1.23:8080/?r=main" {
		t.Fatalf("join-info = %s (VPN interface must not be preferred)", b)
	}

	// DNS rebinding: loopback socket but a foreign Host header
	c2 := newClient(t)
	res, _ = do(t, c2, "POST", ts.URL+"/api/owner/session", nil, nil)
	if res.StatusCode != 200 {
		t.Fatalf("sanity: %d", res.StatusCode)
	}
	req, _ := http.NewRequest("POST", ts.URL+"/api/owner/session", nil)
	req.Host = "evil.example.com"
	req.Header.Set("X-LanShare-Client", "1")
	r, err := newClient(t).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusForbidden {
		t.Errorf("foreign Host header = %d, want 403", r.StatusCode)
	}

	// missing custom header (blocks simple cross-site form posts)
	req, _ = http.NewRequest("POST", ts.URL+"/api/owner/session", nil)
	r, _ = newClient(t).Do(req)
	r.Body.Close()
	if r.StatusCode != http.StatusForbidden {
		t.Errorf("missing client header = %d, want 403", r.StatusCode)
	}
}

func TestCrossOriginWritesAreRejected(t *testing.T) {
	ts, pin := newServer(t)
	c := newClient(t)
	join(t, c, ts, pin)
	res, _ := do(t, c, "POST", ts.URL+"/api/files?name=x.txt", strings.NewReader("x"),
		map[string]string{"Origin": "http://evil.example.com"})
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin upload = %d, want 403", res.StatusCode)
	}
}

func TestPathTraversalInUploadName(t *testing.T) {
	ts, pin := newServer(t)
	c := newClient(t)
	join(t, c, ts, pin)
	res, body := do(t, c, "POST", ts.URL+"/api/files?name=..%2F..%2F..%2Fetc%2Fpasswd", strings.NewReader("x"), nil)
	var f struct{ Name string }
	_ = json.Unmarshal(body, &f)
	if res.StatusCode != 201 || f.Name != "passwd" {
		t.Fatalf("got %d %s", res.StatusCode, body)
	}
}

func TestWebSocketPresenceAndEvents(t *testing.T) {
	ts, pin := newServer(t)
	c := newClient(t)
	join(t, c, ts, pin)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	u := strings.Replace(ts.URL, "http", "ws", 1) + "/ws"
	conn, _, err := websocket.Dial(ctx, u, &websocket.DialOptions{HTTPClient: c})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()

	readType := func() string {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		var m struct{ Type string }
		_ = json.Unmarshal(data, &m)
		return m.Type
	}
	if typ := readType(); typ != "devices" {
		t.Fatalf("first event = %q, want devices (device came online)", typ)
	}

	// presence is visible over REST
	_, b := do(t, c, "GET", ts.URL+"/api/devices", nil, nil)
	var devs []struct {
		Online bool
		Me     bool
	}
	_ = json.Unmarshal(b, &devs)
	if len(devs) != 1 || !devs[0].Online || !devs[0].Me {
		t.Fatalf("devices = %s", b)
	}

	// an upload by another device is announced
	c2 := newClient(t)
	joinNamed(t, c2, ts, pin, "iPhone 15", "iOS")
	do(t, c2, "POST", ts.URL+"/api/files?name=a.txt", strings.NewReader("hello"), nil)
	seen := map[string]bool{}
	for i := 0; i < 6 && !(seen["file.added"] && seen["transfer"]); i++ {
		seen[readType()] = true
	}
	if !seen["file.added"] || !seen["transfer"] {
		t.Fatalf("events seen = %v", seen)
	}

	// closing the socket marks the device offline
	conn.Close(websocket.StatusNormalClosure, "")
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		_, b = do(t, c2, "GET", ts.URL+"/api/devices", nil, nil)
		_ = json.Unmarshal(b, &devs)
		offline := false
		for _, d := range devs {
			if !d.Me && !d.Online {
				offline = true
			}
		}
		if offline {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("device never went offline: %s", b)
}

func TestSecurityHeadersAndStaticUI(t *testing.T) {
	ts, _ := newServer(t)
	res, body := do(t, newClient(t), "GET", ts.URL+"/", nil, nil)
	if res.StatusCode != 200 || !bytes.Contains(body, []byte("LAN Share")) {
		t.Fatalf("index = %d", res.StatusCode)
	}
	if res.Header.Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(res.Header.Get("Content-Security-Policy"), "script-src 'self'") {
		t.Errorf("missing security headers: %v", res.Header)
	}
	for _, p := range []string{"/app.js", "/style.css"} {
		if res, _ := do(t, newClient(t), "GET", ts.URL+p, nil, nil); res.StatusCode != 200 {
			t.Errorf("GET %s = %d", p, res.StatusCode)
		}
	}
}

// ---------------- Phase B ----------------

type fileResp struct {
	ID, Dir, Name string
	Size          int64
	Kind          string
	Thumb, Mine   bool
}

func uploadFile(t *testing.T, c *http.Client, ts *httptest.Server, query, content string) (*http.Response, fileResp) {
	t.Helper()
	res, b := do(t, c, "POST", ts.URL+"/api/files?"+query, strings.NewReader(content), nil)
	var f fileResp
	_ = json.Unmarshal(b, &f)
	return res, f
}

func TestFolderUploadAndZipDownload(t *testing.T) {
	ts, pin := newServer(t)
	c := newClient(t)
	join(t, c, ts, pin)

	var ids []string
	for _, in := range []struct{ q, content string }{
		{"name=a.txt", "AAA"},
		{"name=b.txt&dir=docs%2Fsub", "BBB"},
		{"name=c.txt&dir=..%2F..%2Fdocs", "CCC"},
	} {
		res, f := uploadFile(t, c, ts, in.q, in.content)
		if res.StatusCode != 201 {
			t.Fatalf("upload %s = %d", in.q, res.StatusCode)
		}
		ids = append(ids, f.ID)
	}

	res, body := do(t, c, "GET", ts.URL+"/api/files/zip?ids="+strings.Join(ids, ","), nil, nil)
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "application/zip" ||
		!strings.HasPrefix(res.Header.Get("Content-Disposition"), "attachment") {
		t.Fatalf("zip = %d %v", res.StatusCode, res.Header)
	}
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("response is not a valid zip: %v", err)
	}
	got := map[string]string{}
	for _, zf := range zr.File {
		rc, _ := zf.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		got[zf.Name] = string(b)
	}
	want := map[string]string{"a.txt": "AAA", "docs/sub/b.txt": "BBB", "docs/c.txt": "CCC"}
	if len(got) != 3 || got["a.txt"] != "AAA" || got["docs/sub/b.txt"] != "BBB" || got["docs/c.txt"] != "CCC" {
		t.Fatalf("zip entries = %v, want %v", got, want)
	}

	// errors must stay normal JSON errors, never a download
	res, body = do(t, c, "GET", ts.URL+"/api/files/zip?ids=nope", nil, nil)
	if res.StatusCode != 404 || strings.Contains(res.Header.Get("Content-Disposition"), "attachment") {
		t.Errorf("unknown id = %d %v %s", res.StatusCode, res.Header, body)
	}
	if res, _ := do(t, c, "GET", ts.URL+"/api/files/zip", nil, nil); res.StatusCode != 400 {
		t.Errorf("no ids = %d, want 400", res.StatusCode)
	}

	_, lb := do(t, c, "GET", ts.URL+"/api/files", nil, nil)
	var list []fileResp
	_ = json.Unmarshal(lb, &list)
	dirs := map[string]bool{}
	for _, f := range list {
		dirs[f.Dir] = true
		if !f.Mine || f.Kind != "document" {
			t.Errorf("file DTO = %+v", f)
		}
	}
	if !dirs[""] || !dirs["docs/sub"] || !dirs["docs"] {
		t.Errorf("listed dirs = %v", dirs)
	}
}

func TestNameConflictOverHTTP(t *testing.T) {
	ts, pin := newServer(t)
	c := newClient(t)
	join(t, c, ts, pin)

	_, first := uploadFile(t, c, ts, "name=r.txt", "1")
	_, second := uploadFile(t, c, ts, "name=r.txt", "2")
	if first.Name != "r.txt" || second.Name != "r (2).txt" {
		t.Fatalf("names = %q, %q", first.Name, second.Name)
	}
	_, third := uploadFile(t, c, ts, "name=r.txt&conflict=overwrite", "3")
	if third.Name != "r.txt" || third.ID == first.ID {
		t.Fatalf("overwrite result %+v", third)
	}
	if res, _ := do(t, c, "GET", ts.URL+"/api/files/"+first.ID, nil, nil); res.StatusCode != 404 {
		t.Errorf("overwritten file still downloadable: %d", res.StatusCode)
	}
	if res, _ := uploadFile(t, c, ts, "name=r.txt&conflict=bogus", "x"); res.StatusCode != 400 {
		t.Errorf("bad conflict value = %d, want 400", res.StatusCode)
	}

	// another member must not overwrite it
	c2 := newClient(t)
	joinNamed(t, c2, ts, pin, "iPhone 15", "iOS")
	if res, _ := uploadFile(t, c2, ts, "name=r.txt&conflict=overwrite", "evil"); res.StatusCode != 403 {
		t.Errorf("foreign overwrite = %d, want 403", res.StatusCode)
	}
	_, b := do(t, c, "GET", ts.URL+"/api/files/"+third.ID, nil, nil)
	if string(b) != "3" {
		t.Errorf("file was changed by a refused overwrite: %q", b)
	}
}

func TestThumbnailEndpoint(t *testing.T) {
	ts, pin := newServer(t)
	c := newClient(t)
	join(t, c, ts, pin)

	img := image.NewRGBA(image.Rect(0, 0, 1200, 800))
	var jb bytes.Buffer
	_ = jpeg.Encode(&jb, img, nil)
	_, photo := uploadFile(t, c, ts, "name=photo.jpg", jb.String())
	_, note := uploadFile(t, c, ts, "name=note.txt", "hello")
	_, fake := uploadFile(t, c, ts, "name=fake.png", "not really a png")
	if !photo.Thumb || note.Thumb {
		t.Fatalf("thumb flags: photo=%v note=%v", photo.Thumb, note.Thumb)
	}

	res, body := do(t, c, "GET", ts.URL+"/api/files/"+photo.ID+"/thumb", nil, nil)
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "image/jpeg" {
		t.Fatalf("thumb = %d %v", res.StatusCode, res.Header)
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(body))
	if err != nil || cfg.Width != 360 || cfg.Height != 240 || len(body) >= jb.Len() {
		t.Fatalf("thumbnail %dx%d (%d bytes vs original %d), err %v", cfg.Width, cfg.Height, len(body), jb.Len(), err)
	}
	if res, _ := do(t, c, "GET", ts.URL+"/api/files/"+note.ID+"/thumb", nil, nil); res.StatusCode != http.StatusUnsupportedMediaType {
		t.Errorf("text thumb = %d, want 415", res.StatusCode)
	}
	if res, _ := do(t, c, "GET", ts.URL+"/api/files/"+fake.ID+"/thumb", nil, nil); res.StatusCode != http.StatusUnsupportedMediaType {
		t.Errorf("corrupt png thumb = %d, want 415", res.StatusCode)
	}
	if res, _ := do(t, newClient(t), "GET", ts.URL+"/api/files/"+photo.ID+"/thumb", nil, nil); res.StatusCode != 401 {
		t.Errorf("thumb without session = %d, want 401", res.StatusCode)
	}
}

func TestMessagesAndClipboardOverHTTP(t *testing.T) {
	ts, pin := newServer(t)
	c := newClient(t)
	join(t, c, ts, pin)
	jsonHdr := map[string]string{"Content-Type": "application/json"}

	res, _ := do(t, c, "POST", ts.URL+"/api/messages", strings.NewReader(`{"text":"สวัสดี <b>ทุกคน</b>"}`), jsonHdr)
	if res.StatusCode != 201 {
		t.Fatalf("send message = %d", res.StatusCode)
	}
	_, b := do(t, c, "GET", ts.URL+"/api/messages", nil, nil)
	var msgs []struct{ Text, DeviceName string }
	_ = json.Unmarshal(b, &msgs)
	if len(msgs) != 1 || msgs[0].Text != "สวัสดี <b>ทุกคน</b>" || msgs[0].DeviceName != "Pixel 8" {
		t.Fatalf("messages = %s", b)
	}
	if res, _ := do(t, c, "POST", ts.URL+"/api/messages", strings.NewReader(`{"text":"   "}`), jsonHdr); res.StatusCode != 400 {
		t.Errorf("blank message = %d, want 400", res.StatusCode)
	}
	if res, _ := do(t, c, "POST", ts.URL+"/api/messages", strings.NewReader(`{bad json`), jsonHdr); res.StatusCode != 400 {
		t.Errorf("bad json = %d, want 400", res.StatusCode)
	}
	huge := `{"text":"` + strings.Repeat("a", 200<<10) + `"}`
	if res, _ := do(t, c, "POST", ts.URL+"/api/messages", strings.NewReader(huge), jsonHdr); res.StatusCode != 400 {
		t.Errorf("oversized body = %d, want 400", res.StatusCode)
	}

	if res, _ := do(t, c, "PUT", ts.URL+"/api/clipboard", strings.NewReader(`{"text":"https://example.com"}`), jsonHdr); res.StatusCode != 200 {
		t.Fatalf("set clipboard = %d", res.StatusCode)
	}
	c2 := newClient(t)
	join(t, c2, ts, pin)
	_, b = do(t, c2, "GET", ts.URL+"/api/clipboard", nil, nil)
	var clip struct{ Text, DeviceName string }
	_ = json.Unmarshal(b, &clip)
	if clip.Text != "https://example.com" || clip.DeviceName != "Pixel 8" {
		t.Fatalf("clipboard seen by another device = %s", b)
	}
	// cross-origin writes are refused for the new endpoints too
	res, _ = do(t, c, "PUT", ts.URL+"/api/clipboard", strings.NewReader(`{"text":"x"}`),
		map[string]string{"Content-Type": "application/json", "Origin": "http://evil.example.com"})
	if res.StatusCode != 403 {
		t.Errorf("cross-origin clipboard write = %d, want 403", res.StatusCode)
	}
	if res, _ := do(t, newClient(t), "GET", ts.URL+"/api/messages", nil, nil); res.StatusCode != 401 {
		t.Errorf("messages without session = %d, want 401", res.StatusCode)
	}
}

func TestMessageAndClipboardEventsReachOtherDevices(t *testing.T) {
	ts, pin := newServer(t)
	c1, c2 := newClient(t), newClient(t)
	join(t, c1, ts, pin)
	joinNamed(t, c2, ts, pin, "iPhone 15", "iOS")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, strings.Replace(ts.URL, "http", "ws", 1)+"/ws", &websocket.DialOptions{HTTPClient: c2})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()

	jsonHdr := map[string]string{"Content-Type": "application/json"}
	do(t, c1, "POST", ts.URL+"/api/messages", strings.NewReader(`{"text":"ping"}`), jsonHdr)
	do(t, c1, "PUT", ts.URL+"/api/clipboard", strings.NewReader(`{"text":"clip"}`), jsonHdr)

	seen := map[string]string{}
	for i := 0; i < 8 && (seen["message"] == "" || seen["clipboard"] == ""); i++ {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("read: %v (seen %v)", err, seen)
		}
		var m struct {
			Type string
			Data struct{ Text string }
		}
		_ = json.Unmarshal(data, &m)
		seen[m.Type] = m.Data.Text
	}
	if seen["message"] != "ping" || seen["clipboard"] != "clip" {
		t.Fatalf("events = %v", seen)
	}
}

func ownerClient(t *testing.T, ts *httptest.Server) *http.Client {
	t.Helper()
	c := newClient(t)
	res, b := do(t, c, "POST", ts.URL+"/api/owner/session", nil, nil)
	if res.StatusCode != 200 {
		t.Fatalf("owner session = %d %s", res.StatusCode, b)
	}
	return c
}

func TestPendingDeviceIsForbiddenUntilApproved(t *testing.T) {
	ts, pin := newServer(t)
	owner := ownerClient(t, ts)

	res, b := do(t, owner, "PATCH", ts.URL+"/api/admin/room",
		strings.NewReader(`{"name":"ห้องหลัก","requireApproval":true}`),
		map[string]string{"Content-Type": "application/json"})
	if res.StatusCode != 200 {
		t.Fatalf("enable approval = %d %s", res.StatusCode, b)
	}

	guest := newClient(t)
	res, m := join(t, guest, ts, pin)
	if res.StatusCode != 200 {
		t.Fatalf("join = %d %v", res.StatusCode, m)
	}
	dev, _ := m["device"].(map[string]any)
	id, _ := dev["id"].(string)
	pending, _ := dev["pending"].(bool)
	if !pending || id == "" {
		t.Fatalf("joined device = %v", m["device"])
	}
	res, _ = do(t, guest, "GET", ts.URL+"/api/files", nil, nil)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("pending list files = %d, want 403", res.StatusCode)
	}

	res, b = do(t, owner, "POST", ts.URL+"/api/admin/devices/"+id+"/approve", nil, nil)
	if res.StatusCode != 200 {
		t.Fatalf("approve = %d %s", res.StatusCode, b)
	}
	res, _ = do(t, guest, "GET", ts.URL+"/api/files", nil, nil)
	if res.StatusCode != 200 {
		t.Fatalf("after approve list files = %d", res.StatusCode)
	}
}

func TestGuestLinkUploadOnly(t *testing.T) {
	ts, _ := newServer(t)
	owner := ownerClient(t, ts)
	res, b := do(t, owner, "POST", ts.URL+"/api/admin/guest",
		strings.NewReader(`{"ttlSeconds":3600}`),
		map[string]string{"Content-Type": "application/json"})
	if res.StatusCode != 200 {
		t.Fatalf("rotate guest = %d %s", res.StatusCode, b)
	}
	var out struct {
		GuestURL string `json:"guestUrl"`
	}
	_ = json.Unmarshal(b, &out)
	idx := strings.LastIndex(out.GuestURL, "g=")
	if idx < 0 {
		t.Fatalf("guestUrl = %q", out.GuestURL)
	}
	key := out.GuestURL[idx+2:]
	if i := strings.IndexByte(key, '&'); i >= 0 {
		key = key[:i]
	}

	c := newClient(t)
	res, info := do(t, c, "GET", ts.URL+"/api/guest/"+key+"/info", nil, nil)
	if res.StatusCode != 200 {
		t.Fatalf("guest info = %d %s", res.StatusCode, info)
	}
	res, body := do(t, c, "POST", ts.URL+"/api/guest/"+key+"/join",
		strings.NewReader(`{"deviceName":"iPhone","platform":"iOS"}`),
		map[string]string{"Content-Type": "application/json"})
	if res.StatusCode != 200 {
		t.Fatalf("guest join = %d %s", res.StatusCode, body)
	}
	res, _ = do(t, c, "GET", ts.URL+"/api/files", nil, nil)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("guest list files = %d, want 403", res.StatusCode)
	}
	res, up := do(t, c, "POST", ts.URL+"/api/files?name=from-guest.txt", strings.NewReader("hi"), nil)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("guest upload = %d %s", res.StatusCode, up)
	}
}

func TestOwnerCreatesAndSwitchesRooms(t *testing.T) {
	ts, _ := newServer(t)
	owner := ownerClient(t, ts)
	res, b := do(t, owner, "POST", ts.URL+"/api/admin/rooms",
		strings.NewReader(`{"name":"ทีม B"}`),
		map[string]string{"Content-Type": "application/json"})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create room = %d %s", res.StatusCode, b)
	}
	var rm struct{ ID, Name string }
	_ = json.Unmarshal(b, &rm)
	if rm.Name != "ทีม B" || rm.ID == "" || rm.ID == "main" {
		t.Fatalf("room = %+v", rm)
	}
	res, b = do(t, owner, "POST", ts.URL+"/api/owner/session",
		strings.NewReader(`{"roomId":"`+rm.ID+`"}`),
		map[string]string{"Content-Type": "application/json"})
	if res.StatusCode != 200 {
		t.Fatalf("switch room = %d %s", res.StatusCode, b)
	}
	var me struct {
		Room struct{ ID, Name string }
	}
	_ = json.Unmarshal(b, &me)
	if me.Room.ID != rm.ID {
		t.Fatalf("switched to %+v", me.Room)
	}
}
