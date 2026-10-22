package network

import (
	"context"
	"strconv"
	"strings"

	"lanshare/internal/domain"
)

// CableOpenResult is what the Owner gets after asking the plugged-in phone
// to open the room so it can view files.
type CableOpenResult struct {
	Opened bool
	Device string
	URL    string
	Hint   string
}

// OpenOnCable opens the room in the browser on a USB-plugged Android or iOS
// device. Android uses `adb reverse` plus an VIEW intent. iOS cannot be
// launched reliably over USB, so the URL (USB network or Wi-Fi) is returned
// for the Owner to open on the phone.
func (s *Service) OpenOnCable(ctx context.Context, actor domain.Device) (CableOpenResult, error) {
	if !actor.CanManage() {
		return CableOpenResult{}, domain.ErrForbidden
	}
	st, err := s.Cable(ctx, actor)
	if err != nil {
		return CableOpenResult{}, err
	}
	if st.State == CableIdle {
		return CableOpenResult{}, domain.ErrNotFound
	}
	device := cableOpenDevice(st)
	url := s.cablePageURL(ctx, st, actor.RoomID, device)
	out := CableOpenResult{Device: device, URL: url}

	if device == domain.CableiPhone || device == domain.CableiPad {
		if s.openIOSSafari(ctx, url) {
			out.Opened = true
			return out, nil
		}
		out.Hint = "เปิด Safari บนเครื่อง แล้วไปที่ลิงก์นี้ — ถ้ายังเข้าไม่ได้ ให้เปิด Personal Hotspot ผ่าน USB"
		return out, nil
	}

	if s.openAndroidBrowser(ctx, androidCableURL(s, actor.RoomID)) {
		out.Opened = true
		out.URL = androidCableURL(s, actor.RoomID)
		return out, nil
	}
	out.URL = androidCableURL(s, actor.RoomID)
	if out.URL == "" {
		out.URL = url
	}
	out.Hint = "เปิด USB debugging แล้วกด Trust จากนั้นกดเปิดอีกครั้ง หรือเปิดเบราว์เซอร์ไปที่ลิงก์นี้"
	return out, nil
}

func androidCableURL(s *Service, room domain.RoomID) string {
	return s.URLOn(domain.NetInterface{IP: "127.0.0.1"}, room)
}

func cableOpenDevice(st CableStatus) string {
	for _, p := range st.Phones {
		if p.Device != "" {
			return p.Device
		}
	}
	for _, l := range st.Links {
		if l.Device != "" {
			return l.Device
		}
	}
	return domain.CableGeneric
}

func (s *Service) cablePageURL(ctx context.Context, st CableStatus, room domain.RoomID, device string) string {
	if device == domain.CableAndroid {
		return androidCableURL(s, room)
	}
	for _, l := range st.Links {
		if l.URL != "" && l.IP != "127.0.0.1" {
			return l.URL
		}
	}
	for _, l := range st.Links {
		if l.URL != "" {
			return l.URL
		}
	}
	u, err := s.JoinURL(ctx, room)
	if err != nil {
		return ""
	}
	return u
}

func (s *Service) openAndroidBrowser(ctx context.Context, page string) bool {
	if page == "" {
		return false
	}
	adb := s.findADB(ctx)
	if adb == "" || !adbHasDevice(s.runShort(ctx, adb, "devices")) {
		return false
	}
	port := strconv.Itoa(s.port)
	_ = s.runOK(ctx, adb, "reverse", "tcp:"+port, "tcp:"+port)
	return s.runOK(ctx, adb, "shell", "am", "start",
		"-a", "android.intent.action.VIEW",
		"-d", page)
}

func (s *Service) openIOSSafari(ctx context.Context, page string) bool {
	udid := firstLineID(s.runShort(ctx, "idevice_id", "-l"))
	if udid == "" {
		udid = firstIOSDeviceUDID(s.runShort(ctx, "xcrun", "devicectl", "list", "devices"))
	}
	if udid == "" {
		return false
	}
	if s.runOK(ctx, "xcrun", "devicectl", "device", "process", "launch",
		"--device", udid, "com.apple.mobilesafari") {
		return true
	}
	_ = page
	return false
}

func firstLineID(out []byte) string {
	for _, raw := range strings.Split(string(out), "\n") {
		id := strings.TrimSpace(raw)
		if len(id) >= 24 && !strings.ContainsAny(id, " \t") {
			return id
		}
	}
	return ""
}

func firstIOSDeviceUDID(out []byte) string {
	for _, raw := range strings.Split(string(out), "\n") {
		fields := strings.Fields(raw)
		for _, f := range fields {
			if len(f) >= 24 && strings.Count(f, "-") >= 1 && !strings.Contains(f, ".") {
				return f
			}
		}
	}
	return ""
}
