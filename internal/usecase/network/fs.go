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
	cableListTimeout  = 8 * time.Second
	cablePeekTimeout  = 8 * time.Second
	cablePullTimeout  = 3 * time.Minute
	maxCableFSEntries = 400
	maxCableZipFiles  = 200
	maxCableZipDepth  = 24
	cablePeekLimit    = 16 << 10
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
	if !actor.CanManage() {
		return CableFSList{}, domain.ErrForbidden
	}
	st, err := s.Cable(ctx, actor)
	if err != nil {
		return CableFSList{}, err
	}
	kind := s.cableFSKind(ctx, st)
	if kind == "" {
		return CableFSList{}, domain.ErrNotFound
	}
	rootID, rel, err := splitCablePath(loc)
	if err != nil {
		return CableFSList{}, err
	}
	out := CableFSList{
		Device:  kind,
		Name:    cableFSName(st),
		Path:    loc,
		Entries: []CableFSEntry{},
	}
	if rootID == "" {
		out.Title = "ไฟล์"
		out.Entries = cableFSRoots(kind)
		return out, nil
	}
	out.Parent = cableParent(loc)
	out.Title = cableFSTitle(kind, rootID, rel)
	if kind == domain.CableiPhone || kind == domain.CableiPad {
		return s.listIOS(ctx, out, rootID, rel)
	}
	return s.listAndroid(ctx, out, rootID, rel)
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
		rootID, _, err := splitCablePath(loc)
		if err != nil || rootID == "" {
			return nil, domain.ErrInvalidInput
		}
		seen[loc] = struct{}{}
		out = append(out, loc)
	}
	if len(out) > maxCableZipFiles {
		return nil, domain.ErrInvalidInput
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
	if len(out) == 0 && len(folderLocs) == 0 {
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
	list, err := s.ListCableFS(ctx, actor, loc)
	if err != nil {
		return err
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
		return domain.ErrInvalidInput
	}
	seen[loc] = struct{}{}
	*out = append(*out, CableZipFile{Loc: loc, Name: name})
	return nil
}

func zipFolderPrefix(loc string) string {
	loc = strings.Trim(strings.ReplaceAll(loc, "\\", "/"), "/")
	for _, r := range androidRoots {
		if r.ID == loc {
			return r.Label
		}
	}
	for _, r := range iosRoots {
		if r.ID == loc {
			return r.Label
		}
	}
	if i := strings.LastIndex(loc, "/"); i >= 0 {
		return loc[i+1:]
	}
	return loc
}

func (s *Service) OpenCableFile(ctx context.Context, actor domain.Device, loc string) (CableFile, error) {
	kind, rootID, rel, err := s.cableFileLoc(ctx, actor, loc)
	if err != nil {
		return CableFile{}, err
	}
	if kind == domain.CableiPhone || kind == domain.CableiPad {
		return s.pullIOS(ctx, rootID, rel)
	}
	return s.pullAndroid(ctx, rootID, rel)
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
		if b, err := s.peekAndroid(ctx, rootID, rel, n); err == nil {
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
	kind = s.cableFSKind(ctx, st)
	if kind == "" {
		return "", "", "", domain.ErrNotFound
	}
	rootID, rel, err = splitCablePath(loc)
	if err != nil || rootID == "" || rel == "" {
		return "", "", "", domain.ErrInvalidInput
	}
	return kind, rootID, rel, nil
}

func (s *Service) cableFSKind(ctx context.Context, st CableStatus) string {
	if st.State == CableIdle && !s.androidReady(ctx) {
		return ""
	}
	dev := cableOpenDevice(st)
	if dev == domain.CableiPhone || dev == domain.CableiPad {
		return dev
	}
	if s.androidReady(ctx) {
		return domain.CableAndroid
	}
	if st.State != CableIdle {
		return dev
	}
	return ""
}

func (s *Service) androidReady(ctx context.Context) bool {
	adb := s.findADB(ctx)
	return adb != "" && adbHasDevice(s.runShort(ctx, adb, "devices"))
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

func cableFSRoots(kind string) []CableFSEntry {
	roots := androidRoots
	if kind == domain.CableiPhone || kind == domain.CableiPad {
		roots = iosRoots
	}
	out := make([]CableFSEntry, 0, len(roots))
	for _, r := range roots {
		out = append(out, CableFSEntry{Name: r.Label, Path: r.ID, Dir: true, Kind: "folder"})
	}
	return out
}

func cableFSTitle(kind, rootID, rel string) string {
	roots := androidRoots
	if kind == domain.CableiPhone || kind == domain.CableiPad {
		roots = iosRoots
	}
	label := rootID
	for _, r := range roots {
		if r.ID == rootID {
			label = r.Label
			break
		}
	}
	if rel == "" {
		return label
	}
	return path.Base(rel)
}

func splitCablePath(loc string) (rootID, rel string, err error) {
	loc = strings.Trim(strings.ReplaceAll(loc, "\\", "/"), "/")
	if loc == "" {
		return "", "", nil
	}
	parts := strings.Split(loc, "/")
	for _, p := range parts {
		if p == "" || p == "." || p == ".." {
			return "", "", domain.ErrInvalidInput
		}
	}
	if len(parts) == 1 {
		return parts[0], "", nil
	}
	return parts[0], strings.Join(parts[1:], "/"), nil
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

func joinCablePath(rootID, rel, name string) string {
	p := rootID
	if rel != "" {
		p += "/" + rel
	}
	if name != "" {
		p += "/" + name
	}
	return p
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
