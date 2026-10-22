package httpapi

import (
	"net/http"
	"path/filepath"

	"lanshare/internal/domain"
	"lanshare/internal/usecase/transfer"
)

const shareTargetMaxMemory = 32 << 20 // 32 MiB in memory; larger parts go to temp files

// shareTarget receives files from the Android Web Share Target and publishes
// them into the actor's room, then sends the browser back to the app.
func (s *Server) shareTarget(w http.ResponseWriter, r *http.Request, actor domain.Device) {
	if !actor.CanUpload() {
		s.fail(w, r, domain.ErrForbidden)
		return
	}
	if err := r.ParseMultipartForm(shareTargetMaxMemory); err != nil {
		s.fail(w, r, domain.ErrInvalidInput)
		return
	}
	files := r.MultipartForm.File["file"]
	if len(files) == 0 {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	for _, hdr := range files {
		f, err := hdr.Open()
		if err != nil {
			s.fail(w, r, domain.ErrInvalidInput)
			return
		}
		name := filepath.Base(hdr.Filename)
		_, err = s.Transfer.UploadVerified(r.Context(), actor, transfer.UploadInput{
			Name: name, Size: hdr.Size, Body: f,
		}, nil)
		_ = f.Close()
		if err != nil {
			s.fail(w, r, err)
			return
		}
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
