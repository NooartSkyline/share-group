package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"lanshare/internal/domain"
	"lanshare/internal/usecase/transfer"
)

const (
	// resumeKeyHeader carries the Resume_Key. It is the only place the key is
	// read from: a value in the path, the query string or a body is ignored.
	resumeKeyHeader = "X-Resume-Key"

	// maxUploadJSONBody bounds the JSON bodies of POST /api/uploads and
	// POST /api/uploads/{id}/complete.
	maxUploadJSONBody = 32 << 10

	// idHexLen is the length of a session id and of a Resume_Key: 128 bits as
	// lowercase hex.
	idHexLen = 32
)

// isHex32 reports whether s is exactly 32 lowercase hex characters, the shape
// of every session id and Resume_Key the server issues.
func isHex32(s string) bool {
	if len(s) != idHexLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// resumeKey returns the Resume_Key of the request, or "" when the header is
// missing or is not 32 hex characters (which counts as no key). The value is
// never logged.
func resumeKey(r *http.Request) string {
	if k := r.Header.Get(resumeKeyHeader); isHex32(k) {
		return k
	}
	return ""
}

// sessionRef checks the role, then the shape of the session id, and returns
// the id and key of an upload session request. The role is checked first so a
// device that may not upload gets 403 whatever it names (requirement 5.6); an
// id of the wrong shape gets the same 404 as a session that does not exist.
// On failure the response has been written.
func (s *Server) sessionRef(w http.ResponseWriter, r *http.Request, actor domain.Device) (domain.SessionID, string, bool) {
	if !actor.Role.CanUpload() {
		s.fail(w, r, domain.ErrForbidden)
		return "", "", false
	}
	id := r.PathValue("id")
	if !isHex32(id) {
		s.fail(w, r, domain.ErrNotFound)
		return "", "", false
	}
	return domain.SessionID(id), resumeKey(r), true
}

// decodeUploadJSON decodes one JSON value of at most limit bytes into dst and
// rejects anything after it. It reports whether the body was empty so that
// callers for which a body is optional can tell. On a malformed body the
// response (400) has been written and ok is false.
func decodeUploadJSON(w http.ResponseWriter, r *http.Request, dst any, limit int64) (empty, ok bool) {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return true, true
		}
		writeError(w, http.StatusBadRequest, "bad_request", nil)
		return false, false
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "bad_request", nil)
		return false, false
	}
	return false, true
}

// parseSize reads the declared size from its raw JSON form. Only a plain JSON
// integer is accepted: no quotes, sign, fraction, exponent, or leading zero,
// and nothing above int64 (the use case enforces the real upper bound).
func parseSize(raw json.RawMessage) (int64, bool) {
	if len(raw) == 0 || len(raw) > 19 {
		return 0, false
	}
	for _, c := range raw {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	if len(raw) > 1 && raw[0] == '0' {
		return 0, false
	}
	n, err := json.Number(raw).Int64()
	if err != nil {
		return 0, false
	}
	return n, true
}

// ---- POST /api/uploads ----

type createUploadRequest struct {
	Name     string          `json:"name"`
	Dir      string          `json:"dir"`
	Size     json.RawMessage `json:"size"`
	Conflict *string         `json:"conflict"`
}

type createUploadResponse struct {
	ID        string    `json:"id"`
	ResumeKey string    `json:"resumeKey"`
	Offset    int64     `json:"offset"`
	Size      int64     `json:"size"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// createUpload starts a resumable upload. It is the only response that ever
// carries the Resume_Key.
func (s *Server) createUpload(w http.ResponseWriter, r *http.Request, actor domain.Device) {
	var body createUploadRequest
	if empty, ok := decodeUploadJSON(w, r, &body, maxUploadJSONBody); !ok {
		return
	} else if empty {
		writeError(w, http.StatusBadRequest, "bad_request", nil)
		return
	}
	size, ok := parseSize(body.Size)
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_request", nil)
		return
	}
	var conflict transfer.ConflictPolicy
	if body.Conflict != nil {
		conflict = transfer.ConflictPolicy(*body.Conflict)
		if conflict != transfer.ConflictRename && conflict != transfer.ConflictOverwrite {
			writeError(w, http.StatusBadRequest, "bad_request", nil)
			return
		}
	}
	created, err := s.Transfer.CreateSession(r.Context(), actor, transfer.CreateInput{
		Name: body.Name, Dir: body.Dir, Declared: size, Conflict: conflict,
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, createUploadResponse{
		ID: string(created.ID), ResumeKey: created.ResumeKey, Offset: created.Offset,
		Size: created.Size, ExpiresAt: created.ExpiresAt,
	})
}

// ---- GET /api/uploads/{id} ----

type uploadInfoResponse struct {
	Offset    int64     `json:"offset"`
	Size      int64     `json:"size"`
	ExpiresAt time.Time `json:"expiresAt"`
	SHA256    string    `json:"sha256"`
}

// queryUpload reports the persisted state of a session to its key holder.
func (s *Server) queryUpload(w http.ResponseWriter, r *http.Request, actor domain.Device) {
	id, key, ok := s.sessionRef(w, r, actor)
	if !ok {
		return
	}
	info, err := s.Transfer.QuerySession(r.Context(), actor, id, key)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, uploadInfoResponse{
		Offset: info.Offset, Size: info.Size, ExpiresAt: info.ExpiresAt, SHA256: info.SHA256,
	})
}

// ---- PATCH /api/uploads/{id}?offset=N ----

// parseOffset reads the offset query parameter. A missing, signed, non-numeric
// or overflowing value yields -1, which the use case rejects with
// ErrInvalidInput after it has looked the session up, so that a caller without
// the key learns nothing about the session from the kind of error.
func parseOffset(r *http.Request) int64 {
	v := r.URL.Query().Get("offset")
	if v == "" || len(v) > 19 {
		return -1
	}
	for i := 0; i < len(v); i++ {
		if v[i] < '0' || v[i] > '9' {
			return -1
		}
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return -1
	}
	return n
}

// appendUpload writes the request body into the session after offset.
//
// The body length is r.ContentLength, negative when unknown (chunked), which
// the use case accepts: it then checks the size while reading. Go's server
// has already refused a malformed Content-Length with 400.
//
// An append that ends early records the bytes it got. A stall answers 408
// with the offset to resume from. A request that was interrupted (the client
// left, a newer request replaced it, the server is shutting down) has no
// useful answer, so the connection is dropped without writing one: an empty
// 200 would look like success to a client that is still listening.
func (s *Server) appendUpload(w http.ResponseWriter, r *http.Request, actor domain.Device) {
	id, key, ok := s.sessionRef(w, r, actor)
	if !ok {
		return
	}
	res, err := s.Transfer.Append(r.Context(), actor, id, key, parseOffset(r), r.ContentLength, newAppendBody(w, r.Body))
	if err == nil {
		writeJSON(w, http.StatusOK, map[string]any{"offset": res.Offset})
		return
	}
	var persist *domain.PersistFailedError
	switch {
	case errors.As(err, &persist):
		s.fail(w, r, err) // carries its own offset
	case errors.Is(err, domain.ErrStallTimeout):
		// The read side was cut, so the connection cannot carry another request.
		w.Header().Set("Connection", "close")
		writeError(w, http.StatusRequestTimeout, "stall_timeout", map[string]any{"offset": res.Offset})
	case errors.Is(err, domain.ErrInterrupted), errors.Is(err, context.Canceled):
		panic(http.ErrAbortHandler)
	default:
		s.fail(w, r, err)
	}
}

// ---- POST /api/uploads/{id}/complete ----

// completeUpload publishes a fully received session as a file. The body is
// optional; when it carries sha256 the value must be a valid checksum, and an
// empty string is a 400.
func (s *Server) completeUpload(w http.ResponseWriter, r *http.Request, actor domain.Device) {
	id, key, ok := s.sessionRef(w, r, actor)
	if !ok {
		return
	}
	var body struct {
		SHA256 *string `json:"sha256"`
	}
	if _, ok := decodeUploadJSON(w, r, &body, maxUploadJSONBody); !ok {
		return
	}
	res, err := s.Transfer.CompleteSession(r.Context(), actor, id, key, body.SHA256)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	status := http.StatusCreated
	if res.Repeated {
		status = http.StatusOK
	}
	writeJSON(w, status, toFileDTO(res.File, actor.ID))
}

// ---- DELETE /api/uploads/{id} ----

// cancelUpload discards a session. The Owner of the room needs no key.
func (s *Server) cancelUpload(w http.ResponseWriter, r *http.Request, actor domain.Device) {
	id, key, ok := s.sessionRef(w, r, actor)
	if !ok {
		return
	}
	if err := s.Transfer.CancelSession(r.Context(), actor, id, key); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
