package httpapi

import (
	"errors"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"lanshare/internal/domain"
	"lanshare/internal/usecase/auth"
	"lanshare/internal/usecase/transfer"
)

// ---- DTOs ----

type deviceDTO struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Platform string `json:"platform"`
	Role     string `json:"role"`
	Online   bool   `json:"online"`
	Me       bool   `json:"me"`
}

type fileDTO struct {
	ID           string    `json:"id"`
	Dir          string    `json:"dir"`
	Name         string    `json:"name"`
	Size         int64     `json:"size"`
	Kind         string    `json:"kind"`
	Thumb        bool      `json:"thumb"` // the server can draw a preview image (the client falls back to an icon if it cannot)
	UploaderName string    `json:"uploaderName"`
	Mine         bool      `json:"mine"`
	CreatedAt    time.Time `json:"createdAt"`
	SHA256       string    `json:"sha256"`    // always present; "" when integrity is unknown
	Integrity    string    `json:"integrity"` // verified | unverified | unknown
}

func toDeviceDTO(d domain.Device, me domain.DeviceID) deviceDTO {
	return deviceDTO{string(d.ID), d.Name, d.Platform, string(d.Role), d.Online, d.ID == me}
}

func (s *Server) toFileDTO(f domain.File, me domain.DeviceID) fileDTO {
	integrity := f.IntegrityOrUnknown()
	sum := f.SHA256
	if integrity == domain.IntegrityUnknown {
		sum = "" // a checksum without a known check status is not reported
	}
	return fileDTO{
		ID: string(f.ID), Dir: f.Dir, Name: f.Name, Size: f.Size,
		Kind: string(domain.KindOf(f.Name)), Thumb: s.Transfer.CanPreview(f.Name),
		UploaderName: f.UploaderName, Mine: f.UploaderID == me, CreatedAt: f.CreatedAt,
		SHA256: sum, Integrity: string(integrity),
	}
}

func (s *Server) setSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   int(cookieMaxAge.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
}

// ---- public ----

func (s *Server) roomInfo(w http.ResponseWriter, r *http.Request) {
	rm, err := s.Rooms.Get(r.Context(), domain.RoomID(r.PathValue("room")))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": rm.ID, "name": rm.Name, "pinLength": domain.PINLength})
}

func (s *Server) join(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PIN        string `json:"pin"`
		DeviceName string `json:"deviceName"`
		Platform   string `json:"platform"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	roomID := domain.RoomID(r.PathValue("room"))
	sess, err := s.Auth.Join(r.Context(), auth.JoinInput{
		RoomID:     roomID,
		PIN:        body.PIN,
		DeviceName: body.DeviceName,
		Platform:   body.Platform,
		RemoteAddr: clientIP(r),
		LimiterKey: clientIP(r) + "|" + string(roomID),
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.setSessionCookie(w, sess.Token)
	s.writeMe(w, r, sess.Device)
}

// ownerSession signs in the owner without a PIN, but only for requests that
// really come from this Mac.
func (s *Server) ownerSession(w http.ResponseWriter, r *http.Request) {
	if !isLoopback(r) || !isLocalHost(r) || r.Header.Get(clientHeader) == "" {
		writeError(w, http.StatusForbidden, "forbidden", nil)
		return
	}
	sess, err := s.Auth.OwnerSession(r.Context(), s.DefaultRoom, ownerDeviceNm)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.setSessionCookie(w, sess.Token)
	s.writeMe(w, r, sess.Device)
}

// ---- joined devices ----

func (s *Server) writeMe(w http.ResponseWriter, r *http.Request, actor domain.Device) {
	rm, err := s.Rooms.Get(r.Context(), actor.RoomID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"device": toDeviceDTO(actor, actor.ID),
		"room":   map[string]any{"id": rm.ID, "name": rm.Name},
	})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request, actor domain.Device) {
	s.writeMe(w, r, actor)
}

func (s *Server) listDevices(w http.ResponseWriter, r *http.Request, actor domain.Device) {
	list, err := s.Devices.List(r.Context(), actor)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := make([]deviceDTO, 0, len(list))
	for _, d := range list {
		out = append(out, toDeviceDTO(d, actor.ID))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) listFiles(w http.ResponseWriter, r *http.Request, actor domain.Device) {
	list, err := s.Transfer.List(r.Context(), actor)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := make([]fileDTO, 0, len(list))
	for _, f := range list {
		out = append(out, s.toFileDTO(f, actor.ID))
	}
	writeJSON(w, http.StatusOK, out)
}

// upload streams the raw request body into the room; the body is never
// buffered in memory. Query: name (required), dir (folder), conflict
// ("rename" default, or "overwrite"), sha256 (optional, 64 hex digits).
//
// A sha256 that is present but malformed or empty is refused with 400 before
// the body is read. Content-Length (r.ContentLength, -1 when unknown) goes to
// the use case so the free-space check happens before the first byte.
func (s *Server) upload(w http.ResponseWriter, r *http.Request, actor domain.Device) {
	q := r.URL.Query()
	name := q.Get("name")
	policy := transfer.ConflictPolicy(q.Get("conflict"))
	if name == "" || (policy != "" && policy != transfer.ConflictRename && policy != transfer.ConflictOverwrite) {
		writeError(w, http.StatusBadRequest, "bad_request", nil)
		return
	}
	var checksum *string
	if vals, ok := q["sha256"]; ok {
		if len(vals) != 1 {
			writeError(w, http.StatusBadRequest, "bad_request", nil)
			return
		}
		if _, err := domain.NormalizeChecksum(vals[0]); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", nil)
			return
		}
		checksum = &vals[0]
	}
	f, err := s.Transfer.UploadVerified(r.Context(), actor, transfer.UploadInput{
		Name: name, Dir: q.Get("dir"), Size: r.ContentLength, Conflict: policy, Body: r.Body,
	}, checksum)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, s.toFileDTO(f, actor.ID))
}

// fileChecksum answers with the SHA-256 of a stored file, computing it on the
// first request for a file stored before checksums existed. integrity says
// how far the digest was checked (verified | unverified | unknown). Unlike the
// file list, this endpoint returns the digest even when integrity is unknown,
// because the digest is exactly what the caller asked for.
func (s *Server) fileChecksum(w http.ResponseWriter, r *http.Request, actor domain.Device) {
	info, err := s.Transfer.FileChecksum(r.Context(), actor, domain.FileID(r.PathValue("id")))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"sha256":    info.SHA256,
		"integrity": string(info.Integrity),
	})
}

// downloadZip streams several files as one zip. ids is comma separated.
func (s *Server) downloadZip(w http.ResponseWriter, r *http.Request, actor domain.Device) {
	seen := map[string]bool{}
	var ids []domain.FileID
	for _, part := range strings.Split(r.URL.Query().Get("ids"), ",") {
		part = strings.TrimSpace(part)
		if part != "" && !seen[part] {
			seen[part] = true
			ids = append(ids, domain.FileID(part))
		}
	}
	plan, err := s.Transfer.PlanArchive(r.Context(), actor, ids)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "application/zip")
	h.Set("Content-Disposition", mime.FormatMediaType("attachment",
		map[string]string{"filename": "lanshare-" + time.Now().Format("2006-01-02-1504") + ".zip"}))
	h.Set("Cache-Control", "private, no-store")
	if err := plan.WriteTo(r.Context(), w); err != nil {
		s.Log.Warn("zip download aborted", "err", err)
		// A truncated zip must not look like a finished download.
		panic(http.ErrAbortHandler)
	}
}

func (s *Server) thumbnail(w http.ResponseWriter, r *http.Request, actor domain.Device) {
	b, err := s.Transfer.Thumbnail(r.Context(), actor, domain.FileID(r.PathValue("id")))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "image/jpeg")
	h.Set("Cache-Control", "private, max-age=86400, immutable")
	_, _ = w.Write(b)
}

// ---- messages and clipboard ----

func (s *Server) listMessages(w http.ResponseWriter, r *http.Request, actor domain.Device) {
	list, err := s.Chat.Recent(r.Context(), actor)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) sendMessage(w http.ResponseWriter, r *http.Request, actor domain.Device) {
	var body struct {
		Text string `json:"text"`
	}
	if !decodeJSONLimit(w, r, &body, maxTextBody) {
		return
	}
	m, err := s.Chat.Send(r.Context(), actor, body.Text)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, m)
}

func (s *Server) getClipboard(w http.ResponseWriter, r *http.Request, actor domain.Device) {
	c, err := s.Chat.Clipboard(r.Context(), actor)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) setClipboard(w http.ResponseWriter, r *http.Request, actor domain.Device) {
	var body struct {
		Text string `json:"text"`
	}
	if !decodeJSONLimit(w, r, &body, maxTextBody) {
		return
	}
	c, err := s.Chat.SetClipboard(r.Context(), actor, body.Text)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

// download always sends the file as an attachment. Serving user uploads
// inline would let an uploaded HTML file run inside this origin.
func (s *Server) download(w http.ResponseWriter, r *http.Request, actor domain.Device) {
	dl, err := s.Transfer.Open(r.Context(), actor, domain.FileID(r.PathValue("id")))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer dl.Content.Close()

	ctype := mime.TypeByExtension(filepath.Ext(dl.File.Name))
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	h := w.Header()
	h.Set("Content-Type", ctype)
	h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": dl.File.Name}))
	h.Set("Cache-Control", "private, no-store")
	http.ServeContent(w, r, dl.File.Name, dl.File.CreatedAt, dl.Content)
}

// deleteAllFiles answers DELETE /api/files: the Owner empties their own room.
func (s *Server) deleteAllFiles(w http.ResponseWriter, r *http.Request, actor domain.Device) {
	n, err := s.Transfer.DeleteAll(r.Context(), actor)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"deleted": n})
}

func (s *Server) deleteFile(w http.ResponseWriter, r *http.Request, actor domain.Device) {
	if err := s.Transfer.Delete(r.Context(), actor, domain.FileID(r.PathValue("id"))); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) websocket(w http.ResponseWriter, r *http.Request, actor domain.Device) {
	s.Hub.Serve(w, r, actor)
}

// ---- owner only ----

func (s *Server) joinInfo(w http.ResponseWriter, r *http.Request, actor domain.Device) {
	rm, err := s.Rooms.GetForManager(r.Context(), actor)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	url, err := s.Network.JoinURL(r.Context(), rm.ID)
	if err != nil && !errors.Is(err, domain.ErrNoNetwork) {
		s.fail(w, r, err)
		return
	}
	active, _ := s.Network.Active(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"roomName":  rm.Name,
		"pin":       rm.PIN,
		"url":       url,
		"interface": map[string]string{"name": active.Name, "ip": active.IP},
	})
}

func (s *Server) rotatePIN(w http.ResponseWriter, r *http.Request, actor domain.Device) {
	rm, err := s.Rooms.RotatePIN(r.Context(), actor)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"pin": rm.PIN})
}

func (s *Server) listNetwork(w http.ResponseWriter, r *http.Request, actor domain.Device) {
	opts, err := s.Network.List(r.Context(), actor)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	type item struct {
		Name        string `json:"name"`
		IP          string `json:"ip"`
		Kind        string `json:"kind"`
		Selected    bool   `json:"selected"`
		Recommended bool   `json:"recommended"`
	}
	out := make([]item, 0, len(opts))
	for _, o := range opts {
		out = append(out, item{o.Name, o.IP, string(o.Kind), o.Selected, o.Recommended})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) selectNetwork(w http.ResponseWriter, r *http.Request, actor domain.Device) {
	// Role first: a non-Owner gets 403 even with a malformed body (Req 15.3).
	if !actor.Role.CanManage() {
		s.fail(w, r, domain.ErrForbidden)
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if err := s.Network.Select(r.Context(), actor, body.Name); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) qrPNG(w http.ResponseWriter, r *http.Request, actor domain.Device) {
	if !actor.Role.CanManage() {
		s.fail(w, r, domain.ErrForbidden)
		return
	}
	url, err := s.Network.JoinURL(r.Context(), actor.RoomID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	png, err := s.QR.PNG(url, 320)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(png)
}
