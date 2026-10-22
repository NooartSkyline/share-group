package network

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"lanshare/internal/domain"
)

// adbCableLink tunnels the room to the plugged-in Android over USB with
// `adb reverse`, so the phone can open 127.0.0.1 without Wi-Fi. macOS has no
// RNDIS driver, so Android USB tethering does not get an IP on the Mac.
func (s *Service) adbCableLink(ctx context.Context, room domain.RoomID, phones []CablePhone) (CableLink, bool) {
	adb := s.findADB(ctx)
	if adb == "" {
		return CableLink{}, false
	}
	serials := parseAdbSerials(s.runShort(ctx, adb, "devices"))
	if len(serials) == 0 {
		return CableLink{}, false
	}
	port := strconv.Itoa(s.port)
	if len(serials) == 1 {
		if !s.runOK(ctx, adb, "reverse", "tcp:"+port, "tcp:"+port) {
			return CableLink{}, false
		}
	} else {
		ok := false
		for _, ser := range serials {
			if s.runOK(ctx, adb, "-s", ser, "reverse", "tcp:"+port, "tcp:"+port) {
				ok = true
			}
		}
		if !ok {
			return CableLink{}, false
		}
	}
	device := domain.CableAndroid
	label := "สาย USB"
	if len(phones) > 0 {
		device = phones[0].Device
		if phones[0].Name != "" {
			label = phones[0].Name
		}
	}
	return CableLink{
		Name:   "adb",
		Label:  label,
		IP:     "127.0.0.1",
		Device: device,
		URL:    s.URLOn(domain.NetInterface{IP: "127.0.0.1"}, room),
	}, true
}

func (s *Service) mergeCablePhones(ctx context.Context, usb []CablePhone) []CablePhone {
	out := make([]CablePhone, 0, len(usb)+2)
	var usbAndroid []CablePhone
	for _, p := range usb {
		if p.Device == domain.CableAndroid {
			usbAndroid = append(usbAndroid, p)
			continue
		}
		if p.Path == "" {
			p.Path = p.Device
		}
		out = append(out, p)
	}
	serials := s.adbSerials(ctx)
	if len(serials) == 0 {
		return append(out, usbAndroid...)
	}
	multi := len(serials) > 1
	for i, ser := range serials {
		model, release, kernel := s.androidProps(ctx, ser, multi)
		name := model
		if !multi && i < len(usbAndroid) && usbAndroid[i].Name != "" {
			name = usbAndroid[i].Name
		}
		if name == "" {
			name = ser
		}
		out = append(out, CablePhone{
			Device:  domain.CableAndroid,
			Name:    name,
			Kernel:  kernel,
			Android: release,
			Serial:  ser,
			Path:    androidDevicePath(serials, ser),
		})
	}
	if extra := len(usbAndroid) - len(serials); extra > 0 {
		out = append(out, usbAndroid[len(usbAndroid)-extra:]...)
	}
	return out
}

func (s *Service) androidProps(ctx context.Context, serial string, withSerial bool) (model, release, kernel string) {
	if !withSerial {
		kernel, release = s.androidBuild(ctx)
		adb := s.findADB(ctx)
		if adb != "" {
			model = firstLine(s.runShort(ctx, adb, "shell", "getprop", "ro.product.model"))
		}
		return model, release, kernel
	}
	adb := s.findADB(ctx)
	if adb == "" {
		return "", "", ""
	}
	model = firstLine(s.runShort(ctx, adb, "-s", serial, "shell", "getprop", "ro.product.model"))
	release = firstLine(s.runShort(ctx, adb, "-s", serial, "shell", "getprop", "ro.build.version.release"))
	kernel = firstLine(s.runShort(ctx, adb, "-s", serial, "shell", "uname", "-r"))
	return model, release, kernel
}

func (s *Service) androidBuild(ctx context.Context) (kernel, release string) {
	adb := s.findADB(ctx)
	if adb == "" {
		return "", ""
	}
	if !adbHasDevice(s.runShort(ctx, adb, "devices")) {
		s.mu.Lock()
		s.androidKernel, s.androidRelease = "", ""
		s.mu.Unlock()
		return "", ""
	}
	s.mu.RLock()
	kernel, release = s.androidKernel, s.androidRelease
	s.mu.RUnlock()
	if kernel != "" || release != "" {
		return kernel, release
	}
	kernel = firstLine(s.runShort(ctx, adb, "shell", "uname", "-r"))
	release = firstLine(s.runShort(ctx, adb, "shell", "getprop", "ro.build.version.release"))
	s.mu.Lock()
	s.androidKernel, s.androidRelease = kernel, release
	s.mu.Unlock()
	return kernel, release
}

func firstLine(b []byte) string {
	s := strings.TrimSpace(strings.ReplaceAll(string(b), "\r", ""))
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	return s
}

func (s *Service) findADB(ctx context.Context) string {
	s.mu.RLock()
	cached := s.adb
	s.mu.RUnlock()
	if cached != "" {
		return cached
	}
	for _, p := range adbPaths() {
		if strings.Contains(p, string(os.PathSeparator)) {
			if _, err := os.Stat(p); err != nil {
				continue
			}
		}
		if s.runOK(ctx, p, "version") {
			s.mu.Lock()
			s.adb = p
			s.mu.Unlock()
			return p
		}
	}
	return ""
}

func adbPaths() []string {
	out := []string{"adb", "/opt/homebrew/bin/adb", "/usr/local/bin/adb"}
	if home, err := os.UserHomeDir(); err == nil {
		out = append(out,
			filepath.Join(home, "Library/Android/sdk/platform-tools/adb"),
			filepath.Join(home, "Android/Sdk/platform-tools/adb"),
		)
	}
	return out
}

func adbHasDevice(out []byte) bool {
	return len(parseAdbSerials(out)) > 0
}

func parseAdbSerials(out []byte) []string {
	ids := make([]string, 0, 2)
	for _, raw := range strings.Split(string(out), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "List of devices") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[1] != "device" {
			continue
		}
		if !isAdbSerial(fields[0]) {
			continue
		}
		ids = append(ids, fields[0])
	}
	return ids
}

func isAdbSerial(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			continue
		}
		return false
	}
	return true
}

func parseAndroidDevice(device string) (serial string, ok bool) {
	if device == domain.CableAndroid {
		return "", true
	}
	const prefix = domain.CableAndroid + ":"
	if strings.HasPrefix(device, prefix) {
		serial = device[len(prefix):]
		if isAdbSerial(serial) {
			return serial, true
		}
	}
	return "", false
}

func androidDevicePath(serials []string, serial string) string {
	if len(serials) <= 1 {
		return domain.CableAndroid
	}
	return domain.CableAndroid + ":" + serial
}

func (s *Service) adbSerials(ctx context.Context) []string {
	adb := s.findADB(ctx)
	if adb == "" {
		return nil
	}
	return parseAdbSerials(s.runShort(ctx, adb, "devices"))
}

func (s *Service) adbCmd(ctx context.Context, d time.Duration, device string, args ...string) ([]byte, error) {
	adb := s.findADB(ctx)
	if adb == "" {
		return nil, domain.ErrUnsupported
	}
	if serial, ok := parseAndroidDevice(device); ok && serial != "" {
		args = append([]string{"-s", serial}, args...)
	}
	return s.runTimed(ctx, d, adb, args...)
}

func (s *Service) androidModel(ctx context.Context, serial string) string {
	adb := s.findADB(ctx)
	if adb == "" {
		return serial
	}
	args := []string{"shell", "getprop", "ro.product.model"}
	if serial != "" {
		args = append([]string{"-s", serial}, args...)
	}
	name := firstLine(s.runShort(ctx, adb, args...))
	if name == "" {
		return serial
	}
	return strings.ReplaceAll(name, " ", "_")
}

func (s *Service) runOK(ctx context.Context, name string, args ...string) bool {
	if s.runner == nil {
		return false
	}
	cctx, cancel := context.WithTimeout(ctx, cableCommandTimeout)
	defer cancel()
	_, err := s.runner.Run(cctx, name, args...)
	return err == nil
}
