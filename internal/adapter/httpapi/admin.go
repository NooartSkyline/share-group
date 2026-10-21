package httpapi

import (
	"context"
	"net/http"
	"time"

	"lanshare/internal/domain"
	"lanshare/internal/usecase/diagnostics"
)

// PowerStatusReader reports whether the server is keeping the Mac awake.
type PowerStatusReader interface {
	Status() domain.PowerStatus
}

// DiagnosticsRunner produces the Owner's connectivity report.
// *diagnostics.Service satisfies it.
type DiagnosticsRunner interface {
	Run(ctx context.Context, actor domain.Device, in diagnostics.RunInput) (diagnostics.Report, error)
}

// powerStatus answers GET /api/admin/power for the Owner. Without a reader
// (power management not wired in) it reports domain.PowerDisabled.
func (s *Server) powerStatus(w http.ResponseWriter, r *http.Request, actor domain.Device) {
	if !actor.Role.CanManage() {
		s.fail(w, r, domain.ErrForbidden)
		return
	}
	status := domain.PowerDisabled
	if s.Power != nil {
		status = s.Power.Status()
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": string(status)})
}

type checkDTO struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Hint   string `json:"hint"`
	Reason string `json:"reason"`
}

type diagnosticsDTO struct {
	GeneratedAt time.Time  `json:"generatedAt"`
	Cached      bool       `json:"cached"`
	Checks      []checkDTO `json:"checks"`
}

// diagnostics answers GET /api/admin/diagnostics[?fresh=1] for the Owner. The
// role is checked here before anything else, so a device that is not the
// Owner can never cause a command to run. Whether the request came over TLS
// is taken from the connection, not from a header.
func (s *Server) diagnostics(w http.ResponseWriter, r *http.Request, actor domain.Device) {
	if !actor.Role.CanManage() {
		s.fail(w, r, domain.ErrForbidden)
		return
	}
	if s.Diagnostics == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", nil)
		return
	}
	rep, err := s.Diagnostics.Run(r.Context(), actor, diagnostics.RunInput{
		Fresh:  r.URL.Query().Get("fresh") == "1",
		Secure: r.TLS != nil,
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := diagnosticsDTO{GeneratedAt: rep.GeneratedAt, Cached: rep.Cached, Checks: make([]checkDTO, 0, len(rep.Checks))}
	for _, c := range rep.Checks {
		out.Checks = append(out.Checks, checkDTO{
			ID: c.ID, Status: string(c.Status), Title: c.Title,
			Detail: c.Detail, Hint: c.Hint, Reason: c.Reason,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

type sessionSummaryDTO struct {
	ID             string    `json:"id"`
	Size           int64     `json:"size"`
	Offset         int64     `json:"offset"`
	LastActivityAt time.Time `json:"lastActivityAt"`
}

// listAdminUploads answers GET /api/admin/uploads: the unfinished uploads of
// the Owner's room. The DTO has no Resume_Key and no file name.
func (s *Server) listAdminUploads(w http.ResponseWriter, r *http.Request, actor domain.Device) {
	list, err := s.Transfer.ListSessions(r.Context(), actor)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := make([]sessionSummaryDTO, 0, len(list))
	for _, x := range list {
		out = append(out, sessionSummaryDTO{
			ID: string(x.ID), Size: x.Declared, Offset: x.Offset, LastActivityAt: x.LastActivity,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// cancelAdminUpload answers DELETE /api/admin/uploads/{id}. The role is
// checked before the shape of the id, so a non-Owner gets 403 whatever it
// names; an id of the wrong shape gets the same 404 as one that is unknown.
func (s *Server) cancelAdminUpload(w http.ResponseWriter, r *http.Request, actor domain.Device) {
	if !actor.Role.CanManage() {
		s.fail(w, r, domain.ErrForbidden)
		return
	}
	id := r.PathValue("id")
	if !isHex32(id) {
		s.fail(w, r, domain.ErrNotFound)
		return
	}
	if err := s.Transfer.CancelByOwner(r.Context(), actor, domain.SessionID(id)); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
