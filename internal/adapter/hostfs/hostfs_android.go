//go:build android

package hostfs

import (
	"os"
	"path/filepath"
)

var internalShortcuts = []struct {
	id, label, rel string
}{
	{"download", "ดาวน์โหลด", "Download"},
	{"pictures", "รูปภาพ", "Pictures"},
	{"dcim", "กล้อง", "DCIM"},
	{"movies", "วิดีโอ", "Movies"},
	{"documents", "เอกสาร", "Documents"},
	{"music", "เพลง", "Music"},
	{"all", "ที่เก็บข้อมูล", ""},
}

func detectVolumes() []volume {
	internal := firstDir("/storage/emulated/0", "/sdcard")
	var out []volume
	if internal != "" {
		for _, s := range internalShortcuts {
			abs := internal
			if s.rel != "" {
				abs = filepath.Join(internal, s.rel)
			}
			out = append(out, volume{id: s.id, label: s.label, abs: abs})
		}
	}
	ents, err := os.ReadDir("/storage")
	if err != nil {
		return out
	}
	seen := map[string]struct{}{}
	for _, e := range ents {
		name := e.Name()
		switch name {
		case "emulated", "self", "enc_emulated":
			continue
		}
		p := filepath.Join("/storage", name)
		if st, err := os.Stat(p); err != nil || !st.IsDir() {
			continue
		}
		id := safeID("otg-", name)
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, volume{id: id, label: "USB OTG (" + name + ")", abs: p})
	}
	return out
}

func firstDir(paths ...string) string {
	for _, p := range paths {
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			return p
		}
	}
	return ""
}
