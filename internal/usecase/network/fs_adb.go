package network

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"lanshare/internal/domain"
)

var androidRoots = []cableRoot{
	{ID: "download", Label: "ดาวน์โหลด", Abs: "/sdcard/Download"},
	{ID: "pictures", Label: "รูปภาพ", Abs: "/sdcard/Pictures"},
	{ID: "dcim", Label: "กล้อง", Abs: "/sdcard/DCIM"},
	{ID: "movies", Label: "วิดีโอ", Abs: "/sdcard/Movies"},
	{ID: "documents", Label: "เอกสาร", Abs: "/sdcard/Documents"},
	{ID: "music", Label: "เพลง", Abs: "/sdcard/Music"},
	{ID: "all", Label: "ที่เก็บข้อมูล", Abs: "/sdcard"},
}

func androidAbs(rootID, rel string) (string, error) {
	var root cableRoot
	ok := false
	for _, r := range androidRoots {
		if r.ID == rootID {
			root, ok = r, true
			break
		}
	}
	if !ok {
		return "", domain.ErrInvalidInput
	}
	abs := root.Abs
	if rel != "" {
		abs = abs + "/" + rel
	}
	if !underRoot(abs, root.Abs) {
		return "", domain.ErrInvalidInput
	}
	return abs, nil
}

func (s *Service) listAndroid(ctx context.Context, out CableFSList, rootID, rel string) (CableFSList, error) {
	abs, err := androidAbs(rootID, rel)
	if err != nil {
		return CableFSList{}, err
	}
	adb := s.findADB(ctx)
	if adb == "" || !s.androidReady(ctx) {
		out.Hint = "เปิด USB debugging แล้วกด Trust บนมือถือ จึงจะดูไฟล์ในเครื่องได้"
		return out, nil
	}
	raw, err := s.runTimed(ctx, cableListTimeout, adb, "shell", "ls", "-lp", "--", abs)
	if err != nil {
		out.Hint = "อ่านโฟลเดอร์นี้จากมือถือไม่ได้"
		return out, nil
	}
	text := string(raw)
	if strings.Contains(text, "No such file") || strings.Contains(text, "Permission denied") {
		out.Hint = "ไม่มีโฟลเดอร์นี้บนมือถือ หรือแอปไฟล์ยังไม่อนุญาต"
		return out, nil
	}
	entries := parseAndroidLs(text, rootID, rel)
	if len(entries) > maxCableFSEntries {
		entries = entries[:maxCableFSEntries]
		out.Hint = "แสดง " + strconv.Itoa(maxCableFSEntries) + " รายการแรก"
	}
	out.Entries = entries
	return out, nil
}

func (s *Service) peekAndroid(ctx context.Context, rootID, rel string, n int) ([]byte, error) {
	abs, err := androidAbs(rootID, rel)
	if err != nil {
		return nil, err
	}
	adb := s.findADB(ctx)
	if adb == "" || !s.androidReady(ctx) {
		return nil, domain.ErrUnsupported
	}
	raw, err := s.runTimed(ctx, cablePeekTimeout, adb, "exec-out", "dd", "if="+abs, "bs="+strconv.Itoa(n), "count=1")
	if err != nil {
		return nil, err
	}
	if len(raw) > n {
		raw = raw[:n]
	}
	return raw, nil
}

func (s *Service) pullAndroid(ctx context.Context, rootID, rel string) (CableFile, error) {
	abs, err := androidAbs(rootID, rel)
	if err != nil {
		return CableFile{}, err
	}
	adb := s.findADB(ctx)
	if adb == "" || !s.androidReady(ctx) {
		return CableFile{}, domain.ErrUnsupported
	}
	dir, err := os.MkdirTemp("", "lanshare-cable-*")
	if err != nil {
		return CableFile{}, err
	}
	name := pathBase(rel)
	dst := filepath.Join(dir, name)
	if _, err := s.runTimed(ctx, cablePullTimeout, adb, "pull", "--", abs, dst); err != nil {
		_ = os.RemoveAll(dir)
		return CableFile{}, domain.ErrNotFound
	}
	f, err := os.Open(dst)
	if err != nil {
		_ = os.RemoveAll(dir)
		return CableFile{}, domain.ErrNotFound
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		_ = os.RemoveAll(dir)
		return CableFile{}, err
	}
	return CableFile{Name: name, Size: st.Size(), Content: &removeOnClose{File: f, dir: dir}}, nil
}

func parseAndroidLs(text, rootID, rel string) []CableFSEntry {
	out := make([]CableFSEntry, 0, 32)
	for _, line := range strings.Split(text, "\n") {
		e, ok := parseAndroidLsLine(line)
		if !ok {
			continue
		}
		e.Path = joinCablePath(rootID, rel, e.Name)
		if e.Dir {
			e.Kind = "folder"
		} else {
			e.Kind = string(domain.KindOf(e.Name))
		}
		out = append(out, e)
	}
	return out
}

func parseAndroidLsLine(line string) (CableFSEntry, bool) {
	line = strings.TrimRight(line, "\r")
	if line == "" || strings.HasPrefix(line, "total ") {
		return CableFSEntry{}, false
	}
	fields := strings.Fields(line)
	if len(fields) < 8 {
		return CableFSEntry{}, false
	}
	mode := fields[0]
	if mode[0] != '-' && mode[0] != 'd' && mode[0] != 'l' {
		return CableFSEntry{}, false
	}
	size, err := strconv.ParseInt(fields[4], 10, 64)
	if err != nil {
		return CableFSEntry{}, false
	}
	name := strings.Join(fields[7:], " ")
	name = strings.TrimSuffix(name, "/")
	if name == "" || name == "." || name == ".." || strings.HasPrefix(name, ".") {
		return CableFSEntry{}, false
	}
	for _, r := range name {
		if r < ' ' || r == unicode.ReplacementChar {
			return CableFSEntry{}, false
		}
	}
	dir := mode[0] == 'd'
	if !dir {
		size = size // keep
	} else {
		size = 0
	}
	return CableFSEntry{Name: name, Dir: dir, Size: size}, true
}

func pathBase(p string) string {
	p = strings.Trim(p, "/")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

type removeOnClose struct {
	*os.File
	dir string
}

func (f *removeOnClose) Close() error {
	err := f.File.Close()
	_ = os.RemoveAll(f.dir)
	return err
}
