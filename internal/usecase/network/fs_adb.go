package network

import (
	"context"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"lanshare/internal/domain"
)

var androidRoots = []cableRoot{
	{ID: "apps", Label: "แอป"},
	{ID: "download", Label: "ดาวน์โหลด", Abs: "/sdcard/Download"},
	{ID: "pictures", Label: "รูปภาพ", Abs: "/sdcard/Pictures"},
	{ID: "dcim", Label: "กล้อง", Abs: "/sdcard/DCIM"},
	{ID: "movies", Label: "วิดีโอ", Abs: "/sdcard/Movies"},
	{ID: "documents", Label: "เอกสาร", Abs: "/sdcard/Documents"},
	{ID: "music", Label: "เพลง", Abs: "/sdcard/Music"},
	{ID: "all", Label: "ที่เก็บข้อมูล", Abs: "/sdcard"},
}

const (
	androidAppsRoot     = "apps"
	maxCableCopyApps    = 8
	androidUserPackages = "0"
	cableClipApps       = "apps"
	cableClipFiles      = "files"
)

func androidAbs(rootID, rel string) (string, error) {
	var root cableRoot
	ok := false
	for _, r := range androidRoots {
		if r.ID == rootID {
			root, ok = r, true
			break
		}
	}
	if !ok || root.Abs == "" {
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

func (s *Service) listAndroid(ctx context.Context, out CableFSList, rootID, rel string, ui bool) (CableFSList, error) {
	if rootID == androidAppsRoot {
		return s.listAndroidApps(ctx, out, rel)
	}
	abs, err := androidAbs(rootID, rel)
	if err != nil {
		return CableFSList{}, err
	}
	if s.findADB(ctx) == "" || !s.androidReady(ctx) {
		out.Hint = "เปิด USB debugging แล้วกด Trust บนมือถือ จึงจะดูไฟล์ในเครื่องได้"
		return out, nil
	}
	wait := cableListTimeout
	if !ui {
		wait = cableZipListTimeout
	}
	raw, err := s.adbCmd(ctx, wait, out.Device, "shell", "ls -lp -- "+androidShellQuote(abs))
	if err != nil {
		out.Hint = "อ่านโฟลเดอร์นี้จากมือถือไม่ได้"
		return out, nil
	}
	text := string(raw)
	if strings.Contains(text, "No such file") || strings.Contains(text, "Permission denied") {
		out.Hint = "ไม่มีโฟลเดอร์นี้บนมือถือ หรือแอปไฟล์ยังไม่อนุญาต"
		return out, nil
	}
	entries := parseAndroidLs(text, out.Path)
	if ui && len(entries) > maxCableFSEntries {
		entries = entries[:maxCableFSEntries]
		out.Hint = "แสดง " + strconv.Itoa(maxCableFSEntries) + " รายการแรก"
	}
	out.Entries = entries
	return out, nil
}

func (s *Service) peekAndroid(ctx context.Context, device, rootID, rel string, n int) ([]byte, error) {
	abs, err := s.androidRemoteAbs(ctx, device, rootID, rel)
	if err != nil {
		return nil, err
	}
	if s.findADB(ctx) == "" || !s.androidReady(ctx) {
		return nil, domain.ErrUnsupported
	}
	raw, err := s.adbCmd(ctx, cablePeekTimeout, device, "exec-out", "dd", "if="+abs, "bs="+strconv.Itoa(n), "count=1")
	if err != nil {
		return nil, err
	}
	if len(raw) > n {
		raw = raw[:n]
	}
	return raw, nil
}

func (s *Service) deleteAndroid(ctx context.Context, loc string) error {
	device, rootID, rel, err := splitCablePath(loc)
	if err != nil || rootID == "" || rel == "" {
		return domain.ErrInvalidInput
	}
	if _, ok := parseAndroidDevice(device); !ok {
		return domain.ErrInvalidInput
	}
	if rootID == androidAppsRoot {
		return domain.ErrUnsupported
	}
	abs, err := androidAbs(rootID, rel)
	if err != nil {
		return err
	}
	rootAbs := ""
	for _, r := range androidRoots {
		if r.ID == rootID {
			rootAbs = r.Abs
			break
		}
	}
	if rootAbs == "" || abs == rootAbs || !underRoot(abs, rootAbs) {
		return domain.ErrInvalidInput
	}
	if strings.ContainsAny(abs, "\x00\n\r") {
		return domain.ErrInvalidInput
	}
	if s.findADB(ctx) == "" || !s.androidReady(ctx) {
		return domain.ErrUnsupported
	}
	raw, err := s.adbCmd(ctx, cableDeleteTimeout, device, "shell", "rm -rf -- "+androidShellQuote(abs))
	if err != nil {
		return domain.ErrNotFound
	}
	text := string(raw)
	if strings.Contains(text, "No such file") || strings.Contains(text, "Permission denied") || strings.Contains(text, "Read-only") {
		return domain.ErrNotFound
	}
	return nil
}

func sanitizeCableFolderName(name string) (string, error) {
	name = strings.TrimSpace(strings.ReplaceAll(name, "\\", "/"))
	name = strings.Trim(name, "/")
	if name == "" || name == "." || name == ".." || len(name) > 80 {
		return "", domain.ErrInvalidInput
	}
	if strings.ContainsAny(name, "/\x00\n\r") {
		return "", domain.ErrInvalidInput
	}
	return name, nil
}

// MkdirCableFolder creates a folder on a plugged-in Android so copied files
// can be pasted into it.
func (s *Service) MkdirCableFolder(ctx context.Context, actor domain.Device, parent, name string) (string, error) {
	if !actor.CanManage() {
		return "", domain.ErrForbidden
	}
	name, err := sanitizeCableFolderName(name)
	if err != nil {
		return "", err
	}
	st, err := s.Cable(ctx, actor)
	if err != nil {
		return "", err
	}
	if len(s.cableFSDevices(ctx, st)) == 0 {
		return "", domain.ErrNotFound
	}
	device, rootID, rel, err := splitCablePath(parent)
	if err != nil || device == "" || rootID == "" || rootID == androidAppsRoot {
		return "", domain.ErrInvalidInput
	}
	if device == domain.CableiPhone || device == domain.CableiPad {
		return "", domain.ErrUnsupported
	}
	if _, ok := parseAndroidDevice(device); !ok {
		return "", domain.ErrInvalidInput
	}
	if !s.cableDeviceReady(ctx, device) {
		return "", domain.ErrNotFound
	}
	childRel := name
	if rel != "" {
		childRel = rel + "/" + name
	}
	abs, err := androidAbs(rootID, childRel)
	if err != nil {
		return "", err
	}
	if s.findADB(ctx) == "" || !s.androidReady(ctx) {
		return "", domain.ErrUnsupported
	}
	raw, err := s.adbCmd(ctx, cableListTimeout, device, "shell", "mkdir -p -- "+androidShellQuote(abs))
	if err != nil {
		return "", domain.ErrUnsupported
	}
	text := string(raw)
	if strings.Contains(text, "Permission denied") || strings.Contains(text, "Read-only") || strings.Contains(text, "No such file") {
		return "", domain.ErrUnsupported
	}
	return childCablePath(parent, name), nil
}

func androidShellQuote(p string) string {
	return "'" + strings.ReplaceAll(p, "'", `'\''`) + "'"
}

func (s *Service) pullAndroid(ctx context.Context, device, rootID, rel string) (CableFile, error) {
	abs, err := s.androidRemoteAbs(ctx, device, rootID, rel)
	if err != nil {
		return CableFile{}, err
	}
	if s.findADB(ctx) == "" || !s.androidReady(ctx) {
		return CableFile{}, domain.ErrUnsupported
	}
	dir, err := os.MkdirTemp("", "lanshare-cable-*")
	if err != nil {
		return CableFile{}, err
	}
	name := pathBase(rel)
	dst := filepath.Join(dir, name)
	if _, err := s.adbCmd(ctx, cablePullTimeout, device, "pull", "--", abs, dst); err != nil {
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

func parseAndroidLs(text, parent string) []CableFSEntry {
	out := make([]CableFSEntry, 0, 32)
	for _, line := range strings.Split(text, "\n") {
		e, ok := parseAndroidLsLine(line)
		if !ok {
			continue
		}
		e.Path = childCablePath(parent, e.Name)
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

func (s *Service) androidRemoteAbs(ctx context.Context, device, rootID, rel string) (string, error) {
	if rootID == androidAppsRoot {
		return s.androidAppRemote(ctx, device, rel)
	}
	return androidAbs(rootID, rel)
}

func (s *Service) listAndroidApps(ctx context.Context, out CableFSList, rel string) (CableFSList, error) {
	if s.findADB(ctx) == "" || !s.androidReady(ctx) {
		out.Hint = "เปิด USB debugging แล้วกด Trust บนมือถือ จึงจะดูไฟล์ในเครื่องได้"
		return out, nil
	}
	pkg, apk, err := splitAndroidAppRel(rel)
	if err != nil {
		return CableFSList{}, err
	}
	if apk != "" {
		return CableFSList{}, domain.ErrInvalidInput
	}
	if pkg == "" {
		raw, err := s.adbCmd(ctx, cableListTimeout, out.Device, "shell", "pm", "list", "packages", "-3", "--user", androidUserPackages)
		if err != nil {
			out.Hint = "อ่านรายการแอปจากมือถือไม่ได้"
			return out, nil
		}
		pkgs := parseAndroidPackages(raw)
		if len(pkgs) > maxCableFSEntries {
			pkgs = pkgs[:maxCableFSEntries]
			out.Hint = "แสดง " + strconv.Itoa(maxCableFSEntries) + " รายการแรก"
		} else {
			out.Hint = "เลือกแอป แล้วกดคัดลอก เพื่อติดตั้งลง Android เครื่องอื่น"
		}
		for _, id := range pkgs {
			out.Entries = append(out.Entries, CableFSEntry{
				Name: id, Path: childCablePath(out.Path, id), Dir: true, Kind: "app",
			})
		}
		return out, nil
	}
	paths, err := s.androidAppPaths(ctx, out.Device, pkg)
	if err != nil || len(paths) == 0 {
		out.Hint = "ดึง APK ของแอปนี้ไม่ได้"
		return out, nil
	}
	for _, abs := range paths {
		name := pathBase(abs)
		out.Entries = append(out.Entries, CableFSEntry{
			Name: name, Path: childCablePath(out.Path, name), Dir: false, Size: 0, Kind: "archive",
		})
	}
	return out, nil
}

func (s *Service) androidAppRemote(ctx context.Context, device, rel string) (string, error) {
	pkg, apk, err := splitAndroidAppRel(rel)
	if err != nil || pkg == "" || apk == "" {
		return "", domain.ErrInvalidInput
	}
	paths, err := s.androidAppPaths(ctx, device, pkg)
	if err != nil {
		return "", err
	}
	for _, abs := range paths {
		if pathBase(abs) == apk {
			return abs, nil
		}
	}
	return "", domain.ErrNotFound
}

func (s *Service) androidAppPaths(ctx context.Context, device, pkg string) ([]string, error) {
	if !isAndroidPackage(pkg) {
		return nil, domain.ErrInvalidInput
	}
	raw, err := s.adbCmd(ctx, cableListTimeout, device, "shell", "pm", "path", "--user", androidUserPackages, pkg)
	if err != nil {
		return nil, err
	}
	paths := parsePmPath(raw)
	if len(paths) == 0 {
		return nil, domain.ErrNotFound
	}
	return paths, nil
}

type cableAppClip struct {
	kind    string
	source  string
	names   []string
	pkgs    []cableAppPkg
	files   []cableClipFile
	dir     string
	cut     bool
	origins []string
}

type cableAppPkg struct {
	ID   string
	Name string
	APKs []string
}

type cableClipFile struct {
	Name string
	Path string
}

// CablePasteTarget is an Android that can receive copied apps or files.
type CablePasteTarget struct {
	ID   string
	Name string
}

// CableClipboard is items waiting to be pasted on this or another Android.
type CableClipboard struct {
	Kind       string
	Count      int
	Names      []string
	Source     string
	SourceName string
	Targets    []CablePasteTarget
	Cut        bool
	Origins    []string
}

func (s *Service) CableClipboard(ctx context.Context, actor domain.Device) (CableClipboard, error) {
	if !actor.CanManage() {
		return CableClipboard{}, domain.ErrForbidden
	}
	s.clipMu.Lock()
	clip := s.clip
	s.clipMu.Unlock()
	if clip == nil {
		return CableClipboard{}, nil
	}
	st, err := s.Cable(ctx, actor)
	if err != nil {
		return CableClipboard{}, err
	}
	count := len(clip.pkgs)
	kind := clip.kind
	if count == 0 {
		count = len(clip.files)
		if kind == "" {
			kind = cableClipFiles
		}
	} else if kind == "" {
		kind = cableClipApps
	}
	sameDevice := kind == cableClipFiles || len(clip.files) > 0
	out := CableClipboard{
		Kind:       kind,
		Count:      count,
		Names:      append([]string(nil), clip.names...),
		Source:     clip.source,
		SourceName: s.clipboardSourceName(ctx, st, clip.source),
		Targets:    s.androidPasteTargets(ctx, st, clip.source, sameDevice),
		Cut:        clip.cut,
		Origins:    append([]string(nil), clip.origins...),
	}
	return out, nil
}

// CopyCableItems copies selected apps or files from a plugged-in phone so they
// can be pasted on this Android or another. cut removes the originals after a
// successful paste; apps cannot be cut.
func (s *Service) CopyCableItems(ctx context.Context, actor domain.Device, files, folders []string, cut bool) (CableClipboard, error) {
	all := append(append([]string(nil), files...), folders...)
	if len(all) == 0 {
		return CableClipboard{}, domain.ErrInvalidInput
	}
	if _, _, err := androidCopyPackages(all); err == nil {
		if cut {
			return CableClipboard{}, domain.ErrInvalidInput
		}
		return s.CopyCableApps(ctx, actor, all)
	}
	for _, loc := range all {
		if _, _, err := androidAppPackage(loc); err == nil {
			return CableClipboard{}, domain.ErrInvalidInput
		}
	}
	return s.copyCableFiles(ctx, actor, files, folders, cut)
}

func (s *Service) CopyCableApps(ctx context.Context, actor domain.Device, paths []string) (CableClipboard, error) {
	if !actor.CanManage() {
		return CableClipboard{}, domain.ErrForbidden
	}
	st, err := s.Cable(ctx, actor)
	if err != nil {
		return CableClipboard{}, err
	}
	if len(s.cableFSDevices(ctx, st)) == 0 {
		return CableClipboard{}, domain.ErrNotFound
	}
	source, pkgs, err := androidCopyPackages(paths)
	if err != nil {
		return CableClipboard{}, err
	}
	if !s.cableDeviceReady(ctx, source) {
		return CableClipboard{}, domain.ErrNotFound
	}
	dir, err := os.MkdirTemp("", "lanshare-apps-*")
	if err != nil {
		return CableClipboard{}, err
	}
	copied := make([]cableAppPkg, 0, len(pkgs))
	names := make([]string, 0, len(pkgs))
	for _, pkg := range pkgs {
		if ctx.Err() != nil {
			_ = os.RemoveAll(dir)
			return CableClipboard{}, ctx.Err()
		}
		item, err := s.pullAndroidApp(ctx, source, dir, pkg)
		if err != nil {
			_ = os.RemoveAll(dir)
			return CableClipboard{}, err
		}
		copied = append(copied, item)
		names = append(names, item.Name)
	}
	next := &cableAppClip{kind: cableClipApps, source: source, names: names, pkgs: copied, dir: dir}
	s.clipMu.Lock()
	old := s.clip
	s.clip = next
	s.clipMu.Unlock()
	if old != nil && old.dir != "" {
		_ = os.RemoveAll(old.dir)
	}
	return s.CableClipboard(ctx, actor)
}

func (s *Service) copyCableFiles(ctx context.Context, actor domain.Device, files, folders []string, cut bool) (CableClipboard, error) {
	if !actor.CanManage() {
		return CableClipboard{}, domain.ErrForbidden
	}
	items, err := s.PlanCableZip(ctx, actor, files, folders)
	if err != nil {
		return CableClipboard{}, err
	}
	if len(items) == 0 {
		return CableClipboard{}, domain.ErrInvalidInput
	}
	source := ""
	for _, it := range items {
		device, rootID, _, err := splitCablePath(it.Loc)
		if err != nil || device == "" || rootID == "" || rootID == androidAppsRoot {
			return CableClipboard{}, domain.ErrInvalidInput
		}
		if source == "" {
			source = device
		} else if source != device {
			return CableClipboard{}, domain.ErrInvalidInput
		}
	}
	if !s.cableDeviceReady(ctx, source) {
		return CableClipboard{}, domain.ErrNotFound
	}
	dir, err := os.MkdirTemp("", "lanshare-clip-*")
	if err != nil {
		return CableClipboard{}, err
	}
	copied := make([]cableClipFile, 0, len(items))
	names := make([]string, 0, len(items))
	for _, it := range items {
		if ctx.Err() != nil {
			_ = os.RemoveAll(dir)
			return CableClipboard{}, ctx.Err()
		}
		dst, err := clipLocalPath(dir, it.Name)
		if err != nil {
			_ = os.RemoveAll(dir)
			return CableClipboard{}, err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			_ = os.RemoveAll(dir)
			return CableClipboard{}, err
		}
		cf, err := s.OpenCableFile(ctx, actor, it.Loc)
		if err != nil {
			_ = os.RemoveAll(dir)
			return CableClipboard{}, err
		}
		f, err := os.Create(dst)
		if err != nil {
			_ = cf.Content.Close()
			_ = os.RemoveAll(dir)
			return CableClipboard{}, err
		}
		_, copyErr := io.Copy(f, cf.Content)
		closeErr := f.Close()
		_ = cf.Content.Close()
		if copyErr != nil || closeErr != nil {
			_ = os.RemoveAll(dir)
			if copyErr != nil {
				return CableClipboard{}, copyErr
			}
			return CableClipboard{}, closeErr
		}
		copied = append(copied, cableClipFile{Name: it.Name, Path: dst})
		names = append(names, pathBase(it.Name))
	}
	origins := append(append([]string(nil), files...), folders...)
	next := &cableAppClip{kind: cableClipFiles, source: source, names: names, files: copied, dir: dir, cut: cut, origins: origins}
	s.clipMu.Lock()
	old := s.clip
	s.clip = next
	s.clipMu.Unlock()
	if old != nil && old.dir != "" {
		_ = os.RemoveAll(old.dir)
	}
	return s.CableClipboard(ctx, actor)
}

func clipLocalPath(dir, name string) (string, error) {
	name = strings.Trim(strings.ReplaceAll(name, "\\", "/"), "/")
	if name == "" {
		return "", domain.ErrInvalidInput
	}
	parts := strings.Split(name, "/")
	for _, p := range parts {
		if p == "" || p == "." || p == ".." {
			return "", domain.ErrInvalidInput
		}
	}
	return filepath.Join(append([]string{dir}, parts...)...), nil
}

func (s *Service) PasteCableApps(ctx context.Context, actor domain.Device, dest string) (int, error) {
	if !actor.CanManage() {
		return 0, domain.ErrForbidden
	}
	st, err := s.Cable(ctx, actor)
	if err != nil {
		return 0, err
	}
	s.clipMu.Lock()
	clip := s.clip
	s.clipMu.Unlock()
	if clip == nil || (len(clip.pkgs) == 0 && len(clip.files) == 0) {
		return 0, domain.ErrNotFound
	}
	device, rootID, rel, err := splitCablePath(dest)
	if err != nil || device == "" {
		return 0, domain.ErrInvalidInput
	}
	if !s.cableDeviceReady(ctx, device) {
		return 0, domain.ErrNotFound
	}
	sameDevice := len(clip.files) > 0
	serials := s.adbSerials(ctx)
	if !cablePasteAllowed(clip.source, device, serials, sameDevice) {
		return 0, domain.ErrInvalidInput
	}
	ok := false
	for _, t := range s.androidPasteTargets(ctx, st, clip.source, sameDevice) {
		if t.ID == device {
			ok = true
			break
		}
	}
	if !ok {
		return 0, domain.ErrInvalidInput
	}
	if len(clip.pkgs) > 0 {
		if clip.cut {
			return 0, domain.ErrInvalidInput
		}
		return s.installClipApps(ctx, device, clip.pkgs)
	}
	if rootID == "" {
		rootID = "download"
	}
	if rootID == androidAppsRoot {
		return 0, domain.ErrInvalidInput
	}
	destLoc := device + "/" + rootID
	if rel != "" {
		destLoc += "/" + rel
	}
	if clip.cut && cutPasteNested(clip.origins, destLoc, serials) {
		return 0, domain.ErrInvalidInput
	}
	files := s.renameClipIfTaken(ctx, actor, destLoc, clip.files)
	n, err := s.pushCableFiles(ctx, device, rootID, rel, files)
	if err != nil {
		return n, err
	}
	if !cutPasteSameFolder(clip.origins, destLoc, serials) {
		s.finishCableCut(ctx, actor, clip, n)
	}
	return n, nil
}

func (s *Service) finishCableCut(ctx context.Context, actor domain.Device, clip *cableAppClip, pasted int) {
	if clip == nil || !clip.cut || pasted == 0 || pasted != len(clip.files) || len(clip.origins) == 0 {
		return
	}
	_, _ = s.DeleteCableItems(ctx, actor, clip.origins)
	_ = s.ClearCableClipboard(ctx, actor)
}

func (s *Service) installClipApps(ctx context.Context, dest string, pkgs []cableAppPkg) (int, error) {
	n := 0
	var first error
	for _, pkg := range pkgs {
		if ctx.Err() != nil {
			return n, ctx.Err()
		}
		if err := s.installAndroidApp(ctx, dest, pkg.APKs); err != nil {
			if first == nil {
				first = err
			}
			continue
		}
		n++
	}
	if n == 0 {
		if first != nil {
			return 0, first
		}
		return 0, domain.ErrNotFound
	}
	return n, nil
}

func (s *Service) renameClipIfTaken(ctx context.Context, actor domain.Device, dest string, files []cableClipFile) []cableClipFile {
	if len(files) == 0 {
		return files
	}
	list, err := s.ListCableFS(ctx, actor, dest)
	if err != nil {
		return files
	}
	taken := make(map[string]struct{}, len(list.Entries))
	for _, e := range list.Entries {
		if e.Name != "" {
			taken[strings.ToLower(e.Name)] = struct{}{}
		}
	}
	return uniquifyClipFiles(files, taken)
}

func uniquifyClipFiles(files []cableClipFile, taken map[string]struct{}) []cableClipFile {
	if len(files) == 0 || taken == nil {
		return files
	}
	remap := map[string]string{}
	out := make([]cableClipFile, len(files))
	for i, f := range files {
		top, rest, ok := strings.Cut(f.Name, "/")
		if next, seen := remap[top]; seen {
			if ok {
				out[i] = cableClipFile{Name: next + "/" + rest, Path: f.Path}
			} else {
				out[i] = cableClipFile{Name: next, Path: f.Path}
			}
			continue
		}
		next := domain.UniqueName(top, func(c string) bool {
			_, used := taken[strings.ToLower(c)]
			return used
		})
		taken[strings.ToLower(next)] = struct{}{}
		remap[top] = next
		if ok {
			out[i] = cableClipFile{Name: next + "/" + rest, Path: f.Path}
		} else {
			out[i] = cableClipFile{Name: next, Path: f.Path}
		}
	}
	return out
}

func (s *Service) pushCableFiles(ctx context.Context, device, rootID, rel string, files []cableClipFile) (int, error) {
	if len(files) == 0 {
		return 0, domain.ErrNotFound
	}
	n := 0
	var first error
	for _, f := range files {
		if ctx.Err() != nil {
			return n, ctx.Err()
		}
		remoteRel := f.Name
		if rel != "" {
			remoteRel = rel + "/" + f.Name
		}
		abs, err := androidAbs(rootID, remoteRel)
		if err != nil {
			if first == nil {
				first = err
			}
			continue
		}
		parent := path.Dir(abs)
		if parent != "/" && parent != "." {
			_, _ = s.adbCmd(ctx, cableListTimeout, device, "shell", "mkdir -p -- "+androidShellQuote(parent))
		}
		if _, err := s.adbCmd(ctx, cablePullTimeout, device, "push", "--", f.Path, abs); err != nil {
			if first == nil {
				first = domain.ErrUnsupported
			}
			continue
		}
		n++
	}
	if n == 0 {
		if first != nil {
			return 0, first
		}
		return 0, domain.ErrNotFound
	}
	return n, nil
}

func (s *Service) ClearCableClipboard(ctx context.Context, actor domain.Device) error {
	if !actor.CanManage() {
		return domain.ErrForbidden
	}
	s.clipMu.Lock()
	old := s.clip
	s.clip = nil
	s.clipMu.Unlock()
	if old != nil && old.dir != "" {
		_ = os.RemoveAll(old.dir)
	}
	return nil
}

func (s *Service) pullAndroidApp(ctx context.Context, device, dir, pkg string) (cableAppPkg, error) {
	paths, err := s.androidAppPaths(ctx, device, pkg)
	if err != nil {
		return cableAppPkg{}, err
	}
	pkgDir := filepath.Join(dir, pkg)
	if err := os.MkdirAll(pkgDir, 0o700); err != nil {
		return cableAppPkg{}, err
	}
	apks := make([]string, 0, len(paths))
	for _, abs := range paths {
		name := pathBase(abs)
		if !isAndroidAPKName(name) {
			continue
		}
		dst := filepath.Join(pkgDir, name)
		if _, err := s.adbCmd(ctx, cablePullTimeout, device, "pull", "--", abs, dst); err != nil {
			return cableAppPkg{}, domain.ErrNotFound
		}
		st, err := os.Stat(dst)
		if err != nil || st.IsDir() || st.Size() == 0 {
			return cableAppPkg{}, domain.ErrNotFound
		}
		apks = append(apks, dst)
	}
	if len(apks) == 0 {
		return cableAppPkg{}, domain.ErrNotFound
	}
	return cableAppPkg{ID: pkg, Name: pkg, APKs: apks}, nil
}

func (s *Service) installAndroidApp(ctx context.Context, device string, apks []string) error {
	if len(apks) == 0 {
		return domain.ErrInvalidInput
	}
	args := []string{"install-multiple", "-r", "-g", "--user", androidUserPackages}
	args = append(args, apks...)
	raw, err := s.adbCmd(ctx, cablePullTimeout, device, args...)
	text := strings.ToLower(string(raw))
	if err != nil || strings.Contains(text, "failure") || strings.Contains(text, "error:") {
		if len(apks) == 1 {
			return domain.ErrUnsupported
		}
		raw, err = s.adbCmd(ctx, cablePullTimeout, device, append([]string{"install", "-r", "-g", "--user", androidUserPackages}, apks[0])...)
		text = strings.ToLower(string(raw))
		if err != nil || strings.Contains(text, "failure") || strings.Contains(text, "error:") {
			return domain.ErrUnsupported
		}
	}
	return nil
}

func (s *Service) androidPasteTargets(ctx context.Context, st CableStatus, source string, sameDevice bool) []CablePasteTarget {
	out := make([]CablePasteTarget, 0)
	serials := s.adbSerials(ctx)
	for _, d := range s.cableFSDevices(ctx, st) {
		if _, ok := parseAndroidDevice(d.ID); !ok {
			continue
		}
		if !cablePasteAllowed(source, d.ID, serials, sameDevice) {
			continue
		}
		out = append(out, CablePasteTarget{ID: d.ID, Name: d.Name})
	}
	return out
}

func pasteAcrossDevices(source, dest string, serials []string) bool {
	return cablePasteAllowed(source, dest, serials, false)
}

func cablePasteAllowed(source, dest string, serials []string, sameDevice bool) bool {
	if _, ok := parseAndroidDevice(dest); !ok {
		return false
	}
	if _, ok := parseAndroidDevice(source); ok {
		return androidPasteAllowed(source, dest, serials, sameDevice)
	}
	return source != "" && source != dest
}

func androidPasteAllowed(source, dest string, serials []string, sameDevice bool) bool {
	src, ok1 := parseAndroidDevice(source)
	dst, ok2 := parseAndroidDevice(dest)
	if !ok1 || !ok2 {
		return false
	}
	if src == "" && len(serials) == 1 {
		src = serials[0]
	}
	if dst == "" && len(serials) == 1 {
		dst = serials[0]
	}
	if src == "" || dst == "" {
		return false
	}
	if src == dst {
		return sameDevice
	}
	return true
}

func sameAndroidDevice(a, b string, serials []string) bool {
	sa, oka := parseAndroidDevice(a)
	sb, okb := parseAndroidDevice(b)
	if !oka || !okb {
		return a != "" && a == b
	}
	if sa == "" && len(serials) == 1 {
		sa = serials[0]
	}
	if sb == "" && len(serials) == 1 {
		sb = serials[0]
	}
	if sa == "" && sb == "" {
		return a == domain.CableAndroid && b == domain.CableAndroid
	}
	return sa != "" && sa == sb
}

// cutPasteNested is true when dest is a cut folder or inside one — paste-then-delete
// would remove the files just written.
func cutPasteNested(origins []string, dest string, serials []string) bool {
	return cutPasteRelation(origins, dest, serials) == cutPasteInside
}

// cutPasteSameFolder is true when dest is the folder the items were cut from.
func cutPasteSameFolder(origins []string, dest string, serials []string) bool {
	return cutPasteRelation(origins, dest, serials) == cutPasteParent
}

const (
	cutPasteNone   = 0
	cutPasteParent = 1
	cutPasteInside = 2
)

func cutPasteRelation(origins []string, dest string, serials []string) int {
	destDev, destRoot, destRel, err := splitCablePath(dest)
	if err != nil || destDev == "" || destRoot == "" {
		return cutPasteInside
	}
	destKey := destRoot
	if destRel != "" {
		destKey += "/" + destRel
	}
	rel := cutPasteNone
	for _, origin := range origins {
		origDev, origRoot, origRel, err := splitCablePath(origin)
		if err != nil || origRel == "" {
			return cutPasteInside
		}
		if !sameAndroidDevice(destDev, origDev, serials) {
			continue
		}
		origKey := origRoot + "/" + origRel
		origParent := origRoot
		if dir := path.Dir(origRel); dir != "." && dir != "" {
			origParent += "/" + dir
		}
		if destKey == origKey || strings.HasPrefix(destKey+"/", origKey+"/") {
			return cutPasteInside
		}
		if destKey == origParent {
			rel = cutPasteParent
		}
	}
	return rel
}

func (s *Service) clipboardSourceName(ctx context.Context, st CableStatus, source string) string {
	for _, d := range s.cableFSDevices(ctx, st) {
		if d.ID == source && d.Name != "" {
			return d.Name
		}
	}
	return cableFSDeviceName(st, domain.CableAndroid)
}

func androidCopyPackages(paths []string) (source string, pkgs []string, err error) {
	seen := map[string]struct{}{}
	for _, loc := range paths {
		device, pkg, err := androidAppPackage(loc)
		if err != nil {
			return "", nil, err
		}
		if source == "" {
			source = device
		} else if source != device {
			return "", nil, domain.ErrInvalidInput
		}
		if _, dup := seen[pkg]; dup {
			continue
		}
		seen[pkg] = struct{}{}
		pkgs = append(pkgs, pkg)
	}
	if source == "" || len(pkgs) == 0 {
		return "", nil, domain.ErrInvalidInput
	}
	if len(pkgs) > maxCableCopyApps {
		return "", nil, domain.ErrInvalidInput
	}
	return source, pkgs, nil
}

func androidAppPackage(loc string) (device, pkg string, err error) {
	device, rootID, rel, err := splitCablePath(loc)
	if err != nil || rootID != androidAppsRoot || rel == "" {
		return "", "", domain.ErrInvalidInput
	}
	if _, ok := parseAndroidDevice(device); !ok {
		return "", "", domain.ErrInvalidInput
	}
	pkg, _, err = splitAndroidAppRel(rel)
	if err != nil || pkg == "" {
		return "", "", domain.ErrInvalidInput
	}
	return device, pkg, nil
}

func splitAndroidAppRel(rel string) (pkg, apk string, err error) {
	rel = strings.Trim(rel, "/")
	if rel == "" {
		return "", "", nil
	}
	parts := strings.Split(rel, "/")
	if len(parts) > 2 {
		return "", "", domain.ErrInvalidInput
	}
	if !isAndroidPackage(parts[0]) {
		return "", "", domain.ErrInvalidInput
	}
	if len(parts) == 1 {
		return parts[0], "", nil
	}
	if !isAndroidAPKName(parts[1]) {
		return "", "", domain.ErrInvalidInput
	}
	return parts[0], parts[1], nil
}

func parseAndroidPackages(raw []byte) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, 32)
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if !strings.HasPrefix(line, "package:") {
			continue
		}
		pkg := strings.TrimSpace(strings.TrimPrefix(line, "package:"))
		if i := strings.IndexByte(pkg, '='); i >= 0 {
			pkg = pkg[i+1:]
		}
		if !isAndroidPackage(pkg) {
			continue
		}
		if _, dup := seen[pkg]; dup {
			continue
		}
		seen[pkg] = struct{}{}
		out = append(out, pkg)
	}
	sort.Strings(out)
	return out
}

func parsePmPath(raw []byte) []string {
	out := make([]string, 0, 4)
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if !strings.HasPrefix(line, "package:") {
			continue
		}
		abs := strings.TrimSpace(strings.TrimPrefix(line, "package:"))
		if abs == "" || strings.Contains(abs, "..") || !strings.HasPrefix(abs, "/") {
			continue
		}
		if !isAndroidAPKName(pathBase(abs)) {
			continue
		}
		out = append(out, abs)
	}
	return out
}

func isAndroidPackage(id string) bool {
	if id == "" || len(id) > 210 || strings.ContainsAny(id, "/\\ \t\n") {
		return false
	}
	dot := false
	seg := 0
	for _, r := range id {
		if r == '.' {
			if seg == 0 {
				return false
			}
			dot = true
			seg = 0
			continue
		}
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			seg++
			continue
		}
		return false
	}
	return dot && seg > 0
}

func isAndroidAPKName(name string) bool {
	if name == "base.apk" {
		return true
	}
	return strings.HasPrefix(name, "split_") && strings.HasSuffix(name, ".apk") && !strings.ContainsAny(name, "/\\")
}
