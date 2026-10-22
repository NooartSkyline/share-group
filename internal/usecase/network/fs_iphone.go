package network

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"lanshare/internal/domain"
)

var iosRoots = []cableRoot{
	{ID: "downloads", Label: "ดาวน์โหลด"},
	{ID: "documents", Label: "เอกสาร"},
	{ID: "photos", Label: "รูปภาพ"},
	{ID: "ondevice", Label: "บนเครื่องนี้"},
}

const (
	iosFilesAppID    = "com.apple.DocumentsApp"
	iosFilesGroupID  = "group.com.apple.FileProvider.LocalStorage"
	iosHintNeedTrust = "เสียบ iPhone แล้วกด Trust คอมพิวเตอร์นี้ จึงจะดูไฟล์ในแอป Files ได้"
	iosHintNeedDev   = "เปิด Developer Mode บน iPhone แล้วเชื่อถือคอมพิวเตอร์นี้ เพื่อเรียกดูไฟล์ในแอป Files"
)

func (s *Service) listIOS(ctx context.Context, out CableFSList, rootID, rel string) (CableFSList, error) {
	udid := s.iosConnectedUDID(ctx)
	if udid == "" {
		out.Hint = iosHintNeedTrust
		return out, nil
	}
	domainType, ident, sub, ok := iosDomain(rootID, rel)
	if !ok {
		return CableFSList{}, domain.ErrInvalidInput
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
		out.Hint = iosHintNeedDev
		return out, nil
	}
	entries, err := parseDevicectlFiles(raw, rootID, rel)
	if err != nil {
		out.Hint = iosHintNeedDev
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

func iosDomain(rootID, rel string) (domainType, ident, sub string, ok bool) {
	switch rootID {
	case "downloads":
		return "appGroupDataContainer", iosFilesGroupID, rel, true
	case "documents", "ondevice":
		return "appDataContainer", iosFilesAppID, rel, true
	case "photos":
		return "appDataContainer", iosFilesAppID, joinRel("Photos", rel), true
	default:
		return "", "", "", false
	}
}

func joinRel(base, rel string) string {
	if rel == "" {
		return base
	}
	return base + "/" + rel
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

func parseDevicectlFiles(raw []byte, rootID, rel string) ([]CableFSEntry, error) {
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
		dir := jsonBool(m, "isDirectory", "directory") || strings.EqualFold(jsonString(m, "type", "kind"), "directory")
		size, _ := jsonInt(m, "size", "fileSize")
		if dir {
			size = 0
		}
		kind := "folder"
		if !dir {
			kind = string(domain.KindOf(name))
		}
		out = append(out, CableFSEntry{
			Name: name, Path: joinCablePath(rootID, rel, name), Dir: dir, Size: size, Kind: kind,
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
	for _, k := range keys {
		if s, ok := m[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

func jsonBool(m map[string]any, keys ...string) bool {
	for _, k := range keys {
		if b, ok := m[k].(bool); ok {
			return b
		}
	}
	return false
}

func jsonInt(m map[string]any, keys ...string) (int64, bool) {
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
