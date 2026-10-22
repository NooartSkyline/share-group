package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"lanshare/internal/domain"
)

func chain(h http.Handler, mws ...func(http.Handler) http.Handler) http.Handler {
	for _, m := range mws {
		h = m(h)
	}
	return h
}

// recordPeer notes the source address of every request, static files and
// /ws included, so the reachability check can tell whether any other device
// has reached this server. It is the outermost layer: nothing a later layer
// rejects is hidden from it. The address comes from the TCP connection only;
// forwarded headers are never read, since a client could forge them.
//
// Without a PeerLog or a Clock (as in tests that do not need diagnostics)
// it does nothing.
func (s *Server) recordPeer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.Peers != nil && s.Clock != nil {
			if ip, err := netip.ParseAddr(clientIP(r)); err == nil {
				// An IPv4 client on a dual-stack socket appears as ::ffff:a.b.c.d,
				// and a link-local IPv6 address carries the interface as a zone.
				s.Peers.Record(ip.Unmap().WithZone(""), s.Clock.Now())
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				s.Log.Error("panic in handler", "path", r.URL.Path, "panic", v)
				writeError(w, http.StatusInternalServerError, "internal", nil)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// securityHeaders adds headers that stop uploaded content from being
// interpreted as part of the app.
func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy",
			"default-src 'self'; img-src 'self' data: blob:; connect-src 'self' ws: wss:; "+
				"style-src 'self'; script-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		next.ServeHTTP(w, r)
	})
}

// sameOrigin rejects state-changing requests that come from another origin.
func (s *Server) sameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			if origin := r.Header.Get("Origin"); origin != "" {
				u, err := url.Parse(origin)
				if err != nil || !strings.EqualFold(u.Host, r.Host) {
					writeError(w, http.StatusForbidden, "cross_origin", nil)
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func (s *Server) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		if strings.HasPrefix(r.URL.Path, "/api/") {
			s.Log.Info("request", "method", r.Method, "path", r.URL.Path,
				"status", rec.status, "dur", time.Since(start).Round(time.Millisecond), "from", clientIP(r))
		}
	})
}

func noCache(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		next.ServeHTTP(w, r)
	})
}

type authedHandler func(w http.ResponseWriter, r *http.Request, actor domain.Device)

// authed resolves the session (cookie or Bearer token) before calling next.
func (s *Server) authed(next authedHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := ""
		if c, err := r.Cookie(cookieName); err == nil {
			token = c.Value
		}
		if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
			token = strings.TrimPrefix(h, "Bearer ")
		}
		actor, err := s.Auth.Authenticate(r.Context(), token)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized", nil)
			return
		}
		if actor.Pending && r.URL.Path != "/api/me" && r.URL.Path != "/ws" {
			writeError(w, http.StatusForbidden, "pending", nil)
			return
		}
		next(w, r, actor)
	}
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func isLoopback(r *http.Request) bool {
	ip := net.ParseIP(clientIP(r))
	return ip != nil && ip.IsLoopback()
}

// isLocalHost reports whether the Host header names this machine. Together
// with the loopback check it blocks DNS-rebinding attacks on owner endpoints.
func isLocalHost(r *http.Request) bool {
	host := r.Host
	if h, _, err := net.SplitHostPort(r.Host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

// ---- JSON helpers ----

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code string, extra map[string]any) {
	body := map[string]any{"error": code}
	for k, v := range extra {
		body[k] = v
	}
	writeJSON(w, status, body)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	return decodeJSONLimit(w, r, dst, maxJSONBody)
}

func decodeJSONLimit(w http.ResponseWriter, r *http.Request, dst any, limit int64) bool {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", nil)
		return false
	}
	return true
}

// fail maps a domain error to an HTTP response.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	var inv *domain.InvalidPINError
	var locked *domain.LockedError
	var mismatch *domain.OffsetMismatchError
	var incomplete *domain.IncompleteError
	var space *domain.InsufficientStorageError
	var sum *domain.ChecksumMismatchError
	var persist *domain.PersistFailedError
	switch {
	case errors.As(err, &persist):
		// Checked before the sentinels: its cause may be anything, even a
		// full disk, but the client must resume from the durable offset.
		// The request body may be half read, so the connection is not reused.
		w.Header().Set("Connection", "close")
		writeError(w, http.StatusInternalServerError, "persist_failed", map[string]any{"offset": persist.Offset})
	case errors.As(err, &mismatch):
		writeError(w, http.StatusConflict, "offset_mismatch", map[string]any{"offset": mismatch.Offset})
	case errors.As(err, &incomplete):
		writeError(w, http.StatusConflict, "incomplete", map[string]any{"offset": incomplete.Offset})
	case errors.Is(err, domain.ErrUploadBusy):
		writeError(w, http.StatusConflict, "upload_busy", nil)
	case errors.Is(err, domain.ErrSizeExceeded):
		writeError(w, http.StatusRequestEntityTooLarge, "size_exceeded", nil)
	case errors.Is(err, domain.ErrStallTimeout):
		// The read side was cut, so the connection cannot carry another request.
		w.Header().Set("Connection", "close")
		writeError(w, http.StatusRequestTimeout, "stall_timeout", nil)
	case errors.Is(err, domain.ErrTooManyUploads):
		writeError(w, http.StatusTooManyRequests, "too_many_uploads", nil)
	case errors.As(err, &space):
		writeError(w, http.StatusInsufficientStorage, "insufficient_storage", map[string]any{
			"requiredBytes":  space.Required,
			"availableBytes": space.Available,
		})
	case errors.As(err, &sum):
		writeError(w, http.StatusUnprocessableEntity, "checksum_mismatch", map[string]any{
			"supplied": sum.Supplied,
			"computed": sum.Computed,
		})
	case errors.Is(err, domain.ErrChecksumBusy):
		writeError(w, http.StatusServiceUnavailable, "checksum_busy", nil)
	case errors.Is(err, domain.ErrChecksumFailed):
		writeError(w, http.StatusInternalServerError, "checksum_failed", nil)
	case errors.As(err, &locked):
		secs := int(locked.RetryAfter.Seconds()) + 1
		w.Header().Set("Retry-After", strconv.Itoa(secs))
		writeError(w, http.StatusTooManyRequests, "locked", map[string]any{"retryAfterSeconds": secs})
	case errors.As(err, &inv):
		writeError(w, http.StatusUnauthorized, "invalid_pin", map[string]any{"remaining": inv.Remaining})
	case errors.Is(err, domain.ErrUnauthorized):
		writeError(w, http.StatusUnauthorized, "unauthorized", nil)
	case errors.Is(err, domain.ErrPending):
		writeError(w, http.StatusForbidden, "pending", nil)
	case errors.Is(err, domain.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden", nil)
	case errors.Is(err, domain.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", nil)
	case errors.Is(err, domain.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "bad_request", nil)
	case errors.Is(err, domain.ErrUnsupported):
		writeError(w, http.StatusUnsupportedMediaType, "unsupported", nil)
	case errors.Is(err, domain.ErrNoNetwork):
		writeError(w, http.StatusServiceUnavailable, "no_network", nil)
	case errors.Is(err, context.Canceled):
		// client went away; nothing to send
	default:
		s.Log.Error("request failed", "path", r.URL.Path, "err", err)
		writeError(w, http.StatusInternalServerError, "internal", nil)
	}
}
