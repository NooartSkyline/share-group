package network

import (
	"context"
	"io"
	"path"
	"strings"
	"time"

	"lanshare/internal/domain"
)

const (
	cableListTimeout    = 8 * time.Second
	cablePeekTimeout    = 8 * time.Second
	cablePullTimeout    = 3 * time.Minute
	cableDeleteTimeout  = 1 * time.Minute
	maxCableFSEntries   = 400
	maxCableZipFiles    = 5000
	maxCableZipDepth    = 24
	cableZipListTimeout = 30 * time.Second
	cablePeekLimit      = 16 << 10
)

// CablePeek is the start of a file on the plugged-in phone, for hex preview
// of any extension without pulling the whole file when possible.
type CablePeek struct {
	Name string
	Data []byte
}

// CableFSEntry is one folder or file on a USB-plugged phone.
type CableFSEntry struct {
	Name string
	Path string
	Dir  bool
	Size int64
	Kind string
}

// CableFSList is a Files-app-style listing of the plugged-in phone.
type CableFSList struct {
	Device  string
	Name    string
	Path    string
	Parent  string
	Title   string
	Hint    string
	Entries []CableFSEntry
}

// CableFile is a file pulled from the phone. Close removes the temp copy.
type CableFile struct {
	Name    string
	Size    int64
	Content io.ReadCloser
}

type cableRoot struct {
	ID    string
	Label string
	Icon  string
	Abs   string // Android absolute path; empty for iOS virtual roots
}

func (s *Service) ListCableFS(ctx context.Context, actor domain.Device, loc string) (CableFSList, error) {
	return s.loadCableFS(ctx, actor, loc, true)
}

func (s *Service) loadCableFS(ctx context.Context, actor domain.Device, loc string, ui bool) (CableFSList, error) {
	if !actor.CanManage() {
		return CableFSList{}, domain.ErrForbidden
	}
	st, err := s.Cable(ctx, actor)
	if err != nil {
		return CableFSList{}, err
	}
	devs := s.cableFSDevices(ctx, st)
	if len(devs) == 0 {
		return CableFSList{}, domain.ErrNotFound
	}
	device, rootID, rel, err := splitCablePath(loc)
	if err != nil {
		return CableFSList{}, err
	}
	out := CableFSList{
		Path:    loc,
		Entries: []CableFSEntry{},
	}
	if device == "" {
		out.Title = "ไฟล์"
		out.Name = cableFSName(st)
		for _, d := range devs {
			out.Entries = append(out.Entries, CableFSEntry{Name: d.Name, Path: d.ID, Dir: true, Kind: "folder"})
		}
		return out, nil
	}
	if !s.cableDeviceReady(ctx, device) {
		return CableFSList{}, domain.ErrNotFound
	}
	out.Device = device
	out.Name = cableFSDeviceName(st, device)
	for _, d := range devs {
		if d.ID == device && d.Name != "" {
			out.Name = d.Name
			break
		}
	}
	out.Parent = cableParent(loc)
	if rootID == "" {
		out.Title = out.Name
		if device == domain.CableiPhone || device == domain.CableiPad {
			return s.listIOSApps(ctx, out)
		}
		out.Entries = cableFSRoots(device)
		return out, nil
	}
	out.Title = cableFSTitle(device, rootID, rel)
	if device == domain.CableiPhone || device == domain.CableiPad {
		return s.listIOS(ctx, out, rootID, rel)
	}
	if _, ok := parseAndroidDevice(device); ok {
		return s.listAndroid(ctx, out, rootID, rel, ui)
	}
	return CableFSList{}, domain.ErrInvalidInput
}

// CableZipFile is one file to store in a zip pulled from the phone.
type CableZipFile struct {
	Loc  string
	Name string
}

// SanitizeCableZipPaths keeps unique file or folder paths for a zip download.
func SanitizeCableZipPaths(paths []string) ([]string, error) {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(paths))
	for _, loc := range paths {
		loc = strings.Trim(strings.ReplaceAll(strings.TrimSpace(loc), "\\", "/"), "/")
		if loc == "" {
			continue
		}
		if _, dup := seen[loc]; dup {
			continue
		}
		device, _, _, err := splitCablePath(loc)
		if err != nil || device == "" {
			return nil, domain.ErrInvalidInput
		}
		seen[loc] = struct{}{}
		out = append(out, loc)
	}
	if len(out) > maxCableZipFiles {
		return nil, domain.ErrTooManyItems
	}
	return out, nil
}

// CheckCableFiles verifies the owner can pull each file path from the plugged phone.
func (s *Service) CheckCableFiles(ctx context.Context, actor domain.Device, paths []string) ([]string, error) {
	items, err := s.PlanCableZip(ctx, actor, paths, nil)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Loc
	}
	return out, nil
}

// PlanCableZip expands selected files and folders into zip entries.
func (s *Service) PlanCableZip(ctx context.Context, actor domain.Device, files, folders []string) ([]CableZipFile, error) {
	if !actor.CanManage() {
		return nil, domain.ErrForbidden
	}
	fileLocs, err := SanitizeCableZipPaths(files)
	if err != nil {
		return nil, err
	}
	folderLocs, err := SanitizeCableZipPaths(folders)
	if err != nil {
		return nil, err
	}
	if len(fileLocs)+len(folderLocs) == 0 {
		return nil, domain.ErrInvalidInput
	}
	seen := map[string]struct{}{}
	out := make([]CableZipFile, 0, len(fileLocs))
	for _, loc := range folderLocs {
		if err := s.walkCableZip(ctx, actor, loc, zipFolderPrefix(loc), &out, seen, 0); err != nil {
			return nil, err
		}
	}
	for _, loc := range fileLocs {
		if _, ok := seen[loc]; ok {
			continue
		}
		if _, _, _, err := s.cableFileLoc(ctx, actor, loc); err != nil {
			return nil, err
		}
		if err := appendCableZip(&out, seen, loc, path.Base(loc)); err != nil {
			return nil, err
		}
	}
	if len(out) == 0 {
		return nil, domain.ErrInvalidInput
	}
	return out, nil
}

func (s *Service) walkCableZip(ctx context.Context, actor domain.Device, loc, prefix string, out *[]CableZipFile, seen map[string]struct{}, depth int) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if depth > maxCableZipDepth {
		return nil
	}
	list, err := s.loadCableFS(ctx, actor, loc, false)
	if err != nil {
		return err
	}
	if len(list.Entries) == 0 && strings.TrimSpace(list.Hint) != "" {
		if depth == 0 {
			return domain.ErrNotFound
		}
		return nil
	}
	for _, e := range list.Entries {
		if e.Dir {
			child := e.Name
			if prefix != "" {
				child = prefix + "/" + e.Name
			}
			if err := s.walkCableZip(ctx, actor, e.Path, child, out, seen, depth+1); err != nil {
				return err
			}
			continue
		}
		name := e.Name
		if prefix != "" {
			name = prefix + "/" + e.Name
		}
		if err := appendCableZip(out, seen, e.Path, name); err != nil {
			return err
		}
	}
	return nil
}

func appendCableZip(out *[]CableZipFile, seen map[string]struct{}, loc, name string) error {
	if _, ok := seen[loc]; ok {
		return nil
	}
	if len(*out) >= maxCableZipFiles {
		return domain.ErrTooManyItems
	}
	seen[loc] = struct{}{}
	*out = append(*out, CableZipFile{Loc: loc, Name: name})
	return nil
}

func zipFolderPrefix(loc string) string {
	device, rootID, rel, err := splitCablePath(loc)
	if err != nil {
		return path.Base(loc)
	}
	if rel != "" {
		return path.Base(rel)
	}
	if rootID != "" {
		return cableFSTitle(device, rootID, "")
	}
	return cableFSDeviceName(CableStatus{}, device)
}

// DeleteCableItems removes files or folders on the plugged phone. Shortcut
// roots such as ดาวน์โหลด are refused. iPhone is unsupported over USB.
func (s *Service) DeleteCableItems(ctx context.Context, actor domain.Device, paths []string) (int, error) {
	if !actor.CanManage() {
		return 0, domain.ErrForbidden
	}
	clean, err := SanitizeCableZipPaths(paths)
	if err != nil || len(clean) == 0 {
		return 0, domain.ErrInvalidInput
	}
	st, err := s.Cable(ctx, actor)
	if err != nil {
		return 0, err
	}
	if len(s.cableFSDevices(ctx, st)) == 0 {
		return 0, domain.ErrNotFound
	}
	n := 0
	var first error
	for _, loc := range clean {
		if ctx.Err() != nil {
			return n, ctx.Err()
		}
		device, _, _, err := splitCablePath(loc)
		if err != nil {
			if first == nil {
				first = err
			}
			continue
		}
		if device == domain.CableiPhone || device == domain.CableiPad {
			if first == nil {
				first = domain.ErrUnsupported
			}
			continue
		}
		if err := s.deleteAndroid(ctx, loc); err != nil {
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

func (s *Service) OpenCableFile(ctx context.Context, actor domain.Device, loc string) (CableFile, error) {
	kind, rootID, rel, err := s.cableFileLoc(ctx, actor, loc)
	if err != nil {
		return CableFile{}, err
	}
	if kind == domain.CableiPhone || kind == domain.CableiPad {
		return s.pullIOS(ctx, rootID, rel)
	}
	return s.pullAndroid(ctx, kind, rootID, rel)
}

// PeekCableFile returns the first bytes of a file on the phone so every
// extension can be previewed. Android uses a short dd read; otherwise the
// file is pulled and truncated.
func (s *Service) PeekCableFile(ctx context.Context, actor domain.Device, loc string, n int) (CablePeek, error) {
	if n <= 0 || n > cablePeekLimit {
		n = cablePeekLimit
	}
	kind, rootID, rel, err := s.cableFileLoc(ctx, actor, loc)
	if err != nil {
		return CablePeek{}, err
	}
	name := pathBase(rel)
	if kind != domain.CableiPhone && kind != domain.CableiPad {
		if b, err := s.peekAndroid(ctx, kind, rootID, rel, n); err == nil {
			return CablePeek{Name: name, Data: b}, nil
		}
	}
	cf, err := s.OpenCableFile(ctx, actor, loc)
	if err != nil {
		return CablePeek{}, err
	}
	defer cf.Content.Close()
	b, err := io.ReadAll(io.LimitReader(cf.Content, int64(n)))
	if err != nil {
		return CablePeek{}, err
	}
	return CablePeek{Name: cf.Name, Data: b}, nil
}

func (s *Service) cableFileLoc(ctx context.Context, actor domain.Device, loc string) (kind, rootID, rel string, err error) {
	if !actor.CanManage() {
		return "", "", "", domain.ErrForbidden
	}
	st, err := s.Cable(ctx, actor)
	if err != nil {
		return "", "", "", err
	}
	if len(s.cableFSDevices(ctx, st)) == 0 {
		return "", "", "", domain.ErrNotFound
	}
	kind, rootID, rel, err = splitCablePath(loc)
	if err != nil || kind == "" || rootID == "" || rel == "" {
		return "", "", "", domain.ErrInvalidInput
	}
	if !s.cableDeviceReady(ctx, kind) {
		return "", "", "", domain.ErrNotFound
	}
	return kind, rootID, rel, nil
}

func (s *Service) cableFSKind(ctx context.Context, st CableStatus) string {
	devs := s.cableFSDevices(ctx, st)
	if len(devs) == 0 {
		return ""
	}
	return devs[0].ID
}

type cableFSDevice struct {
	ID   string
	Name string
}

func (s *Service) cableFSDevices(ctx context.Context, st CableStatus) []cableFSDevice {
	out := make([]cableFSDevice, 0, 2)
	serials := s.adbSerials(ctx)
	switch len(serials) {
	case 0:
	case 1:
		out = append(out, cableFSDevice{ID: domain.CableAndroid, Name: cableFSDeviceName(st, domain.CableAndroid)})
	default:
		for _, ser := range serials {
			name := s.androidModel(ctx, ser)
			if name == "" {
				name = ser
			}
			out = append(out, cableFSDevice{ID: androidDevicePath(serials, ser), Name: name})
		}
	}
	if s.iosConnectedUDID(ctx) != "" {
		id := domain.CableiPhone
		for _, p := range st.Phones {
			if p.Device == domain.CableiPad {
				id = domain.CableiPad
				break
			}
		}
		out = append(out, cableFSDevice{ID: id, Name: cableFSDeviceName(st, id)})
	}
	return out
}

func (s *Service) cableDeviceReady(ctx context.Context, device string) bool {
	if serial, ok := parseAndroidDevice(device); ok {
		serials := s.adbSerials(ctx)
		if serial == "" {
			return len(serials) == 1
		}
		for _, id := range serials {
			if id == serial {
				return true
			}
		}
		return false
	}
	switch device {
	case domain.CableiPhone, domain.CableiPad:
		return s.iosConnectedUDID(ctx) != ""
	default:
		return false
	}
}

func (s *Service) androidReady(ctx context.Context) bool {
	return len(s.adbSerials(ctx)) > 0
}

func cableFSName(st CableStatus) string {
	for _, p := range st.Phones {
		if p.Name != "" {
			return p.Name
		}
	}
	for _, l := range st.Links {
		if l.Label != "" {
			return l.Label
		}
	}
	return "สาย USB"
}

func cableFSDeviceName(st CableStatus, id string) string {
	for _, p := range st.Phones {
		if p.Device == id && p.Name != "" {
			return p.Name
		}
	}
	switch id {
	case domain.CableiPad:
		return "iPad"
	case domain.CableiPhone:
		return "iPhone"
	default:
		return "Android"
	}
}

func cableFSRoots(device string) []CableFSEntry {
	out := make([]CableFSEntry, 0, len(androidRoots))
	for _, r := range androidRoots {
		out = append(out, CableFSEntry{Name: r.Label, Path: childCablePath(device, r.ID), Dir: true, Kind: "folder"})
	}
	return out
}

func cableFSTitle(kind, rootID, rel string) string {
	label := rootID
	if _, ok := parseAndroidDevice(kind); ok {
		for _, r := range androidRoots {
			if r.ID == rootID {
				label = r.Label
				break
			}
		}
	}
	if rel == "" {
		return label
	}
	return path.Base(rel)
}

func splitCablePath(loc string) (device, rootID, rel string, err error) {
	loc = strings.Trim(strings.ReplaceAll(loc, "\\", "/"), "/")
	if loc == "" {
		return "", "", "", nil
	}
	parts := strings.Split(loc, "/")
	for _, p := range parts {
		if p == "" || p == "." || p == ".." {
			return "", "", "", domain.ErrInvalidInput
		}
	}
	if !isCableDevice(parts[0]) {
		return "", "", "", domain.ErrInvalidInput
	}
	device = parts[0]
	if len(parts) == 1 {
		return device, "", "", nil
	}
	rootID = parts[1]
	if len(parts) == 2 {
		return device, rootID, "", nil
	}
	return device, rootID, strings.Join(parts[2:], "/"), nil
}

func isCableDevice(s string) bool {
	if _, ok := parseAndroidDevice(s); ok {
		return true
	}
	return s == domain.CableiPhone || s == domain.CableiPad
}

func cableParent(loc string) string {
	loc = strings.Trim(strings.ReplaceAll(loc, "\\", "/"), "/")
	if loc == "" {
		return ""
	}
	i := strings.LastIndex(loc, "/")
	if i <= 0 {
		return ""
	}
	return loc[:i]
}

func childCablePath(parent, name string) string {
	parent = strings.Trim(parent, "/")
	name = strings.Trim(name, "/")
	if parent == "" {
		return name
	}
	if name == "" {
		return parent
	}
	return parent + "/" + name
}

func underRoot(abs, root string) bool {
	abs = path.Clean("/" + strings.TrimPrefix(abs, "/"))
	root = path.Clean("/" + strings.TrimPrefix(root, "/"))
	return abs == root || strings.HasPrefix(abs, root+"/")
}

func (s *Service) runTimed(ctx context.Context, d time.Duration, name string, args ...string) ([]byte, error) {
	if s.runner == nil {
		return nil, domain.ErrUnsupported
	}
	cctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	return s.runner.Run(cctx, name, args...)
}
