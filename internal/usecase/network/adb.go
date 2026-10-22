package network

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"

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
	if !adbHasDevice(s.runShort(ctx, adb, "devices")) {
		return CableLink{}, false
	}
	port := strconv.Itoa(s.port)
	if !s.runOK(ctx, adb, "reverse", "tcp:"+port, "tcp:"+port) {
		return CableLink{}, false
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

func (s *Service) fillAndroidKernel(ctx context.Context, phones []CablePhone) []CablePhone {
	hasAndroid := false
	for _, p := range phones {
		if p.Device == domain.CableAndroid {
			hasAndroid = true
			break
		}
	}
	if !hasAndroid {
		return phones
	}
	kernel, release := s.androidBuild(ctx)
	if kernel == "" && release == "" {
		return phones
	}
	for i := range phones {
		if phones[i].Device != domain.CableAndroid {
			continue
		}
		phones[i].Kernel = kernel
		phones[i].Android = release
	}
	return phones
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
	for _, raw := range strings.Split(string(out), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "List of devices") {
			continue
		}
		if strings.Contains(line, "\tdevice") || strings.HasSuffix(line, " device") {
			return true
		}
	}
	return false
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
