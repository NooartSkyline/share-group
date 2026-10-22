// Package httpapi exposes the use cases over HTTP and serves the web UI.
package httpapi

import (
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"lanshare/internal/adapter/ws"
	"lanshare/internal/domain"
	"lanshare/internal/usecase/auth"
	"lanshare/internal/usecase/chat"
	"lanshare/internal/usecase/device"
	"lanshare/internal/usecase/network"
	"lanshare/internal/usecase/port"
	"lanshare/internal/usecase/room"
	"lanshare/internal/usecase/transfer"
)

const (
	cookieName    = "lanshare_token"
	clientHeader  = "X-LanShare-Client"
	maxJSONBody   = 4 << 10
	maxTextBody   = 128 << 10 // messages and clipboard: 20k characters of Thai is ~60 KB
	cookieMaxAge  = 7 * 24 * time.Hour
	ownerDeviceNm = "MacBook (เครื่องนี้)"
)

// Deps are the collaborators of the HTTP server.
type Deps struct {
	Auth     *auth.Service
	Rooms    *room.Service
	Devices  *device.Service
	Transfer *transfer.Service
	Chat     *chat.Service
	Network  *network.Service
	QR       port.QRGenerator
	Hub      *ws.Hub
	Power    PowerStatusReader // optional; nil reports "disabled"
	// Diagnostics runs the Owner's connectivity checks. Optional: when nil,
	// GET /api/admin/diagnostics answers 503 to the Owner (403 to anyone else).
	Diagnostics DiagnosticsRunner
	// Peers and Clock feed the reachability check. Both are optional; the
	// recordPeer middleware records nothing unless both are set.
	Peers       port.PeerLog
	Clock       port.Clock
	Web         fs.FS
	DefaultRoom domain.RoomID
	Log         *slog.Logger
}

type Server struct {
	Deps
}

func NewServer(d Deps) *Server { return &Server{Deps: d} }

// Handler builds the router with all middleware applied.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// public
	mux.HandleFunc("GET /api/rooms/{room}/info", s.roomInfo)
	mux.HandleFunc("POST /api/rooms/{room}/join", s.join)
	mux.HandleFunc("POST /api/owner/session", s.ownerSession)

	// any joined device
	mux.HandleFunc("GET /api/me", s.authed(s.me))
	mux.HandleFunc("GET /api/devices", s.authed(s.listDevices))
	mux.HandleFunc("GET /api/files", s.authed(s.listFiles))
	mux.HandleFunc("POST /api/files", s.authed(s.upload))
	mux.HandleFunc("GET /api/files/zip", s.authed(s.downloadZip))
	mux.HandleFunc("GET /api/files/{id}", s.authed(s.download))
	mux.HandleFunc("GET /api/files/{id}/thumb", s.authed(s.thumbnail))
	mux.HandleFunc("GET /api/files/{id}/sha256", s.authed(s.fileChecksum))
	mux.HandleFunc("DELETE /api/files/{id}", s.authed(s.deleteFile))
	mux.HandleFunc("DELETE /api/files", s.authed(s.deleteAllFiles))
	mux.HandleFunc("POST /api/uploads", s.authed(s.createUpload))
	mux.HandleFunc("GET /api/uploads/{id}", s.authed(s.queryUpload))
	mux.HandleFunc("PATCH /api/uploads/{id}", s.authed(s.appendUpload))
	mux.HandleFunc("POST /api/uploads/{id}/complete", s.authed(s.completeUpload))
	mux.HandleFunc("DELETE /api/uploads/{id}", s.authed(s.cancelUpload))
	mux.HandleFunc("GET /api/messages", s.authed(s.listMessages))
	mux.HandleFunc("POST /api/messages", s.authed(s.sendMessage))
	mux.HandleFunc("GET /api/clipboard", s.authed(s.getClipboard))
	mux.HandleFunc("PUT /api/clipboard", s.authed(s.setClipboard))
	mux.HandleFunc("GET /ws", s.authed(s.websocket))

	// owner only (enforced inside the use cases)
	mux.HandleFunc("GET /api/admin/join-info", s.authed(s.joinInfo))
	mux.HandleFunc("POST /api/admin/pin/rotate", s.authed(s.rotatePIN))
	mux.HandleFunc("GET /api/admin/network", s.authed(s.listNetwork))
	mux.HandleFunc("POST /api/admin/network", s.authed(s.selectNetwork))
	mux.HandleFunc("GET /api/admin/qr.png", s.authed(s.qrPNG))
	mux.HandleFunc("GET /api/admin/power", s.authed(s.powerStatus))
	mux.HandleFunc("GET /api/admin/diagnostics", s.authed(s.diagnostics))
	mux.HandleFunc("GET /api/admin/uploads", s.authed(s.listAdminUploads))
	mux.HandleFunc("DELETE /api/admin/uploads/{id}", s.authed(s.cancelAdminUpload))

	// web UI
	registerPWA(mux, s.Web)
	mux.Handle("GET /", noCache(http.FileServerFS(s.Web)))

	// chain wraps in order, so the last middleware is the outermost.
	return chain(mux, s.recoverPanic, s.securityHeaders, s.sameOrigin, s.accessLog, s.recordPeer)
}
