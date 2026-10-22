package httpapi

import (
	"net/http"

	"lanshare/internal/domain"
	"lanshare/internal/usecase/transfer"
)

// shortcuts resolves a loopback caller as the Mac Owner without a PIN, so
// Shortcuts and the menu bar can talk to the server on this machine.
func (s *Server) shortcuts(next authedHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isLoopback(r) || !isLocalHost(r) {
			writeError(w, http.StatusForbidden, "forbidden", nil)
			return
		}
		roomID := s.DefaultRoom
		if id := r.URL.Query().Get("room"); id != "" {
			if !domain.ValidRoomID(id) {
				s.fail(w, r, domain.ErrInvalidInput)
				return
			}
			roomID = domain.RoomID(id)
		}
		sess, err := s.Auth.OwnerSession(r.Context(), roomID, ownerDeviceNm)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		next(w, r, sess.Device)
	}
}

func (s *Server) shortcutsInfo(w http.ResponseWriter, r *http.Request, actor domain.Device) {
	s.joinInfo(w, r, actor)
}

func (s *Server) shortcutsQR(w http.ResponseWriter, r *http.Request, actor domain.Device) {
	s.qrPNG(w, r, actor)
}

func (s *Server) shortcutsFiles(w http.ResponseWriter, r *http.Request, actor domain.Device) {
	s.listFiles(w, r, actor)
}

func (s *Server) shortcutsSend(w http.ResponseWriter, r *http.Request, actor domain.Device) {
	name := r.URL.Query().Get("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "bad_request", nil)
		return
	}
	f, err := s.Transfer.UploadVerified(r.Context(), actor, transfer.UploadInput{
		Name: name, Size: r.ContentLength, Body: r.Body,
	}, nil)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, s.toFileDTO(f, actor.ID))
}
