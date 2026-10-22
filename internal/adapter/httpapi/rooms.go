package httpapi

import (
	"net/http"
	"time"

	"lanshare/internal/domain"
	"lanshare/internal/usecase/room"
)

func (s *Server) guestInfo(w http.ResponseWriter, r *http.Request) {
	rm, err := s.Rooms.LookupGuest(r.Context(), r.PathValue("key"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": rm.ID, "name": rm.Name, "guest": true})
}

func (s *Server) guestJoin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DeviceName string `json:"deviceName"`
		Platform   string `json:"platform"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	sess, err := s.Auth.JoinGuest(r.Context(), r.PathValue("key"), body.DeviceName, body.Platform, clientIP(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.setSessionCookie(w, r, sess.Token)
	s.writeMe(w, r, sess.Device)
}

func roomPublic(r domain.Room) map[string]any {
	out := map[string]any{
		"id": r.ID, "name": r.Name, "createdAt": r.CreatedAt,
		"quotaBytes": r.QuotaBytes, "fileTTLSeconds": int64(r.FileTTL / time.Second),
		"requireApproval": r.RequireApproval,
		"guestUntil":      nil,
		"guestMax":        r.GuestMax,
	}
	if !r.GuestUntil.IsZero() && r.GuestKey != "" {
		out["guestUntil"] = r.GuestUntil
	}
	return out
}

func (s *Server) listRooms(w http.ResponseWriter, r *http.Request, actor domain.Device) {
	list, err := s.Rooms.ListForOwner(r.Context(), actor)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := make([]map[string]any, 0, len(list))
	for _, rm := range list {
		item := roomPublic(rm)
		item["current"] = rm.ID == actor.RoomID
		out = append(out, item)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createRoom(w http.ResponseWriter, r *http.Request, actor domain.Device) {
	var body struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	rm, err := s.Rooms.Create(r.Context(), actor, body.Name)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, roomPublic(rm))
}

func (s *Server) deleteRoom(w http.ResponseWriter, r *http.Request, actor domain.Device) {
	id := domain.RoomID(r.PathValue("id"))
	next, err := s.Rooms.Delete(r.Context(), actor, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.Transfer.PurgeRoom(r.Context(), actor, id); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.Devices.PurgeRoom(r.Context(), actor, id); err != nil {
		s.fail(w, r, err)
		return
	}
	if actor.RoomID != id {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	sess, err := s.Auth.OwnerSession(r.Context(), next.ID, ownerDeviceNm)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.setSessionCookie(w, r, sess.Token)
	s.writeMe(w, r, sess.Device)
}

func (s *Server) patchRoom(w http.ResponseWriter, r *http.Request, actor domain.Device) {
	var body struct {
		Name            string `json:"name"`
		QuotaBytes      int64  `json:"quotaBytes"`
		FileTTLSeconds  int64  `json:"fileTTLSeconds"`
		RequireApproval bool   `json:"requireApproval"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	rm, err := s.Rooms.UpdateSettings(r.Context(), actor, room.Settings{
		Name:            body.Name,
		QuotaBytes:      body.QuotaBytes,
		FileTTL:         time.Duration(body.FileTTLSeconds) * time.Second,
		RequireApproval: body.RequireApproval,
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, roomPublic(rm))
}

func (s *Server) rotateGuest(w http.ResponseWriter, r *http.Request, actor domain.Device) {
	var body struct {
		TTLSeconds int64 `json:"ttlSeconds"`
		MaxBytes   int64 `json:"maxBytes"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	ttl := time.Duration(body.TTLSeconds) * time.Second
	if body.TTLSeconds == 0 {
		ttl = time.Hour
	}
	rm, err := s.Rooms.RotateGuest(r.Context(), actor, ttl, body.MaxBytes)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	url, _ := s.Network.GuestURL(r.Context(), rm.GuestKey)
	out := roomPublic(rm)
	out["guestUrl"] = url
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) clearGuest(w http.ResponseWriter, r *http.Request, actor domain.Device) {
	if _, err := s.Rooms.ClearGuest(r.Context(), actor); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) approveDevice(w http.ResponseWriter, r *http.Request, actor domain.Device) {
	d, err := s.Devices.Approve(r.Context(), actor, domain.DeviceID(r.PathValue("id")))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toDeviceDTO(d, actor.ID))
}

func (s *Server) rejectDevice(w http.ResponseWriter, r *http.Request, actor domain.Device) {
	if err := s.Devices.Reject(r.Context(), actor, domain.DeviceID(r.PathValue("id"))); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) patchDevice(w http.ResponseWriter, r *http.Request, actor domain.Device) {
	var body struct {
		Role string `json:"role"`
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	id := domain.DeviceID(r.PathValue("id"))
	var (
		d   domain.Device
		err error
	)
	switch {
	case body.Role != "":
		d, err = s.Devices.SetRole(r.Context(), actor, id, domain.Role(body.Role))
	case body.Name != "":
		d, err = s.Devices.Rename(r.Context(), actor, id, body.Name)
	default:
		writeError(w, http.StatusBadRequest, "bad_request", nil)
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toDeviceDTO(d, actor.ID))
}
