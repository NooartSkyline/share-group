package network

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"lanshare/internal/domain"
)

const (
	iosHintNeedTrust   = "เสียบ iPhone แล้วกด Trust คอมพิวเตอร์นี้ จึงจะดูไฟล์ได้"
	iosHintNeedDev     = "เปิด Developer Mode บน iPhone แล้วเชื่อถือคอมพิวเตอร์นี้ จึงจะดูไฟล์ในแอปที่ติดตั้งจาก Xcode ได้"
	iosHintNoApps      = "ดูไฟล์ได้เฉพาะแอปที่ติดตั้งจาก Xcode บน iPhone นี้ แอป Files ของระบบเรียกดูผ่าน USB ไม่ได้"
	iosHintDevAppsOnly = "เรียกดูได้เฉพาะแอปที่ติดตั้งจาก Xcode ไม่ใช่แอป Files ของระบบ"
	iosHintNoAccess    = "แอปนี้ไม่ให้เรียกดูไฟล์ผ่าน USB"
)

type iosApp struct {
	ID   string
	Name string
}

func (s *Service) listIOSApps(ctx context.Context, out CableFSList) (CableFSList, error) {
	udid := s.iosConnectedUDID(ctx)
	if udid == "" {
		out.Hint = iosHintNeedTrust
		return out, nil
	}
	raw, err := s.runTimed(ctx, cableListTimeout, "xcrun", "devicectl", "device", "info", "apps",
		"--device", udid, "--require-container-access", "--json-output", "-")
	if err != nil || looksLikeDevicectlError(raw) {
		if looksLikeDeveloperMode(raw) || looksLikeDeveloperModeErr(err) {
			out.Hint = iosHintNeedDev
			return out, nil
		}
		out.Hint = iosHintNoApps
		return out, nil
	}
	apps, err := parseDevicectlApps(raw)
	if err != nil {
		out.Hint = iosHintNoApps
		return out, nil
	}
	for _, a := range apps {
		out.Entries = append(out.Entries, CableFSEntry{
			Name: a.Name, Path: childCablePath(out.Path, a.ID), Dir: true, Kind: "folder",
		})
	}
	if len(apps) == 0 {
		out.Hint = iosHintNoApps
	} else {
		out.Hint = iosHintDevAppsOnly
	}
	return out, nil
}

func (s *Service) listIOS(ctx context.Context, out CableFSList, rootID, rel string) (CableFSList, error) {
	udid := s.iosConnectedUDID(ctx)
	if udid == "" {
		out.Hint = iosHintNeedTrust
		return out, nil
	}
	domainType, ident, sub, ok := iosDomain(rootID, rel)
	if !ok {
		out.Hint = iosHintNoAccess
		return out, nil
	}
	if rel == "" {
		if name := s.iosAppName(ctx, udid, rootID); name != "" {
			out.Title = name
		}
	}
	args := []string{"devicectl", "device", "info", "files",
		"--device", udid,
		"--domain-type", domainType,
		"--domain-identifier", ident,
		"--no-recurse",
		"--json-output", "-",
	}
	if sub != "" {
		args = append(args, "--subdirectory", sub)
	}
	raw, err := s.runTimed(ctx, cableListTimeout, "xcrun", args...)
	if err != nil || looksLikeDevicectlError(raw) {
		out.Hint = iosHintNoAccess
		return out, nil
	}
	entries, err := parseDevicectlFiles(raw, out.Path)
	if err != nil {
		out.Hint = iosHintNoAccess
		return out, nil
	}
	if len(entries) > maxCableFSEntries {
		entries = entries[:maxCableFSEntries]
	}
	out.Entries = entries
	return out, nil
}

func (s *Service) pullIOS(ctx context.Context, rootID, rel string) (CableFile, error) {
	udid := s.iosConnectedUDID(ctx)
	if udid == "" {
		return CableFile{}, domain.ErrUnsupported
	}
	domainType, ident, sub, ok := iosDomain(rootID, rel)
	if !ok || sub == "" {
		return CableFile{}, domain.ErrInvalidInput
	}
	dir, err := os.MkdirTemp("", "lanshare-cable-*")
	if err != nil {
		return CableFile{}, err
	}
	name := pathBase(rel)
	dst := filepath.Join(dir, name)
	args := []string{"devicectl", "device", "copy", "from",
		"--device", udid,
		"--domain-type", domainType,
		"--domain-identifier", ident,
		"--source", sub,
		"--destination", dst,
		"--timeout", "120",
	}
	if _, err := s.runTimed(ctx, cablePullTimeout, "xcrun", args...); err != nil {
		_ = os.RemoveAll(dir)
		return CableFile{}, domain.ErrNotFound
	}
	f, err := os.Open(dst)
	if err != nil {
		_ = os.RemoveAll(dir)
		return CableFile{}, domain.ErrNotFound
	}
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		f.Close()
		_ = os.RemoveAll(dir)
		return CableFile{}, domain.ErrNotFound
	}
	return CableFile{Name: name, Size: st.Size(), Content: &removeOnClose{File: f, dir: dir}}, nil
}

func (s *Service) iosAppName(ctx context.Context, udid, bundleID string) string {
	raw, err := s.runTimed(ctx, cableListTimeout, "xcrun", "devicectl", "device", "info", "apps",
		"--device", udid, "--require-container-access", "--json-output", "-")
	if err != nil {
		return ""
	}
	apps, err := parseDevicectlApps(raw)
	if err != nil {
		return ""
	}
	for _, a := range apps {
		if a.ID == bundleID {
			return a.Name
		}
	}
	return ""
}

func iosDomain(rootID, rel string) (domainType, ident, sub string, ok bool) {
	if !isIOSBundleID(rootID) {
		return "", "", "", false
	}
	return "appDataContainer", rootID, rel, true
}

func isIOSBundleID(id string) bool {
	if id == "" || len(id) > 210 {
		return false
	}
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func (s *Service) iosConnectedUDID(ctx context.Context) string {
	if id := firstLineID(s.runShort(ctx, "idevice_id", "-l")); id != "" {
		return id
	}
	raw, err := s.runTimed(ctx, cableListTimeout, "xcrun", "devicectl", "list", "devices", "--json-output", "-")
	if err != nil {
		return firstIOSDeviceUDID(s.runShort(ctx, "xcrun", "devicectl", "list", "devices"))
	}
	return firstPhysicalUDID(raw)
}

func firstPhysicalUDID(raw []byte) string {
	var doc map[string]any
	if json.Unmarshal(raw, &doc) != nil {
		return ""
	}
	result, _ := doc["result"].(map[string]any)
	devices, _ := result["devices"].([]any)
	for _, item := range devices {
		m, _ := item.(map[string]any)
		if m == nil {
			continue
		}
		conn := nestedString(m, "connectionProperties", "transportType")
		state := nestedString(m, "connectionProperties", "pairingState")
		reality := nestedString(m, "hardwareProperties", "reality")
		if strings.EqualFold(reality, "simulated") {
			continue
		}
		if strings.Contains(strings.ToLower(conn), "wired") || strings.Contains(strings.ToLower(conn), "usb") || conn != "" {
			if strings.EqualFold(state, "unpaired") {
				continue
			}
			if id := nestedString(m, "hardwareProperties", "udid"); id != "" {
				return id
			}
			if id, _ := m["identifier"].(string); id != "" {
				return id
			}
		}
	}
	return ""
}

func nestedString(m map[string]any, keys ...string) string {
	cur := any(m)
	for _, k := range keys {
		obj, _ := cur.(map[string]any)
		if obj == nil {
			return ""
		}
		cur = obj[k]
	}
	s, _ := cur.(string)
	return s
}

func looksLikeDevicectlError(raw []byte) bool {
	s := string(raw)
	return strings.Contains(s, `"outcome" : "failed"`) || strings.Contains(s, `"outcome":"failed"`) ||
		strings.Contains(s, "not supported") || strings.Contains(s, "ERROR:")
}

func looksLikeDeveloperMode(raw []byte) bool {
	s := strings.ToLower(string(raw))
	return strings.Contains(s, "developer mode") || strings.Contains(s, "developermode")
}

func looksLikeDeveloperModeErr(err error) bool {
	return err != nil && looksLikeDeveloperMode([]byte(err.Error()))
}

func parseDevicectlApps(raw []byte) ([]iosApp, error) {
	if looksLikeDevicectlError(raw) {
		return nil, domain.ErrUnsupported
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	result, _ := doc["result"].(map[string]any)
	if result == nil {
		return nil, domain.ErrUnsupported
	}
	list := firstSlice(result, "apps")
	out := make([]iosApp, 0, len(list))
	for _, item := range list {
		m, _ := item.(map[string]any)
		if m == nil {
			continue
		}
		if accessible, ok := m["containerAccessible"].(bool); ok && !accessible {
			continue
		}
		id := jsonString(m, "bundleIdentifier", "bundleID")
		if !isIOSBundleID(id) {
			continue
		}
		name := jsonString(m, "name")
		if name == "" {
			name = id
		}
		out = append(out, iosApp{ID: id, Name: name})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name == out[j].Name {
			return out[i].ID < out[j].ID
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func parseDevicectlFiles(raw []byte, parent string) ([]CableFSEntry, error) {
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	result, _ := doc["result"].(map[string]any)
	if result == nil {
		return nil, domain.ErrUnsupported
	}
	list := firstSlice(result, "files", "items", "entries")
	out := make([]CableFSEntry, 0, len(list))
	for _, item := range list {
		m, _ := item.(map[string]any)
		if m == nil {
			continue
		}
		name := jsonString(m, "name", "fileName", "lastPathComponent")
		if name == "" || strings.HasPrefix(name, ".") {
			continue
		}
		res, _ := m["resources"].(map[string]any)
		meta, _ := m["metadata"].(map[string]any)
		if jsonBool(m, "isHidden") || jsonBool(res, "isHidden") {
			continue
		}
		dir := jsonBool(m, "isDirectory", "directory") || jsonBool(res, "isDirectory") ||
			strings.EqualFold(jsonString(m, "type", "kind"), "directory")
		size, ok := jsonInt(m, "size", "fileSize")
		if !ok {
			size, _ = jsonInt(meta, "size", "fileSize")
		}
		if dir {
			size = 0
		}
		kind := "folder"
		if !dir {
			kind = string(domain.KindOf(name))
		}
		out = append(out, CableFSEntry{
			Name: name, Path: childCablePath(parent, name), Dir: dir, Size: size, Kind: kind,
		})
	}
	return out, nil
}

func firstSlice(m map[string]any, keys ...string) []any {
	for _, k := range keys {
		if s, ok := m[k].([]any); ok {
			return s
		}
	}
	return nil
}

func jsonString(m map[string]any, keys ...string) string {
	if m == nil {
		return ""
	}
	for _, k := range keys {
		if s, ok := m[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

func jsonBool(m map[string]any, keys ...string) bool {
	if m == nil {
		return false
	}
	for _, k := range keys {
		if b, ok := m[k].(bool); ok {
			return b
		}
	}
	return false
}

func jsonInt(m map[string]any, keys ...string) (int64, bool) {
	if m == nil {
		return 0, false
	}
	for _, k := range keys {
		switch v := m[k].(type) {
		case float64:
			return int64(v), true
		case json.Number:
			n, err := v.Int64()
			return n, err == nil
		case string:
			n, err := strconv.ParseInt(v, 10, 64)
			return n, err == nil
		}
	}
	return 0, false
}
