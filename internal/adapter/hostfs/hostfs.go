// Package hostfs implements port.HostBrowser on this machine's directories.
package hostfs

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"lanshare/internal/domain"
	"lanshare/internal/usecase/port"
)

const extraFileName = "host-roots.json"

type extraRoot struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Path  string `json:"path"`
}

type volume struct {
	id, label, abs string
}

// Browser lists host volumes. detect is swapped in tests.
type Browser struct {
	detect    func() []volume
	extraFile string
}

var _ port.HostBrowser = (*Browser)(nil)

// New returns a Browser for this operating system. dataDir may hold
// host-roots.json written by the Android shell after a SAF folder pick.
func New(dataDir string) *Browser {
	extra := ""
	if dataDir != "" {
		extra = filepath.Join(dataDir, extraFileName)
	}
	return &Browser{detect: detectVolumes, extraFile: extra}
}

func (b *Browser) volumes() []volume {
	if b == nil {
		return nil
	}
	var out []volume
	if b.detect != nil {
		out = b.detect()
	}
	seenID := make(map[string]struct{}, len(out))
	seenAbs := make(map[string]struct{}, len(out))
	for _, v := range out {
		seenID[v.id] = struct{}{}
		if abs, err := filepath.Abs(v.abs); err == nil {
			seenAbs[abs] = struct{}{}
		}
	}
	for _, extra := range loadExtra(b.extraFile) {
		if extra.id == "" || extra.abs == "" {
			continue
		}
		if _, ok := seenID[extra.id]; ok {
			continue
		}
		abs, err := filepath.Abs(extra.abs)
		if err != nil {
			continue
		}
		if _, ok := seenAbs[abs]; ok {
			continue
		}
		seenID[extra.id] = struct{}{}
		seenAbs[abs] = struct{}{}
		out = append(out, extra)
	}
	return out
}

func (b *Browser) find(id string) (volume, bool) {
	for _, v := range b.volumes() {
		if v.id == id {
			return v, true
		}
	}
	return volume{}, false
}

func (b *Browser) Volumes(context.Context) ([]port.HostVolume, error) {
	raw := b.volumes()
	out := make([]port.HostVolume, 0, len(raw))
	for _, v := range raw {
		out = append(out, port.HostVolume{ID: v.id, Label: v.label})
	}
	return out, nil
}

func (b *Browser) List(_ context.Context, volumeID, rel string) ([]port.HostEntry, error) {
	abs, err := b.resolve(volumeID, rel)
	if err != nil {
		return nil, err
	}
	ents, err := os.ReadDir(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return []port.HostEntry{}, nil
		}
		return nil, err
	}
	out := make([]port.HostEntry, 0, len(ents))
	for _, e := range ents {
		name := e.Name()
		if name == "" || strings.HasPrefix(name, ".") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		item := port.HostEntry{Name: name, Dir: e.IsDir() || isDir(info)}
		if !item.Dir {
			item.Size = info.Size()
		}
		out = append(out, item)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Dir != out[j].Dir {
			return out[i].Dir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

func (b *Browser) Open(_ context.Context, volumeID, rel string) (port.ReadSeekCloser, int64, error) {
	if strings.TrimSpace(rel) == "" {
		return nil, 0, domain.ErrInvalidInput
	}
	abs, err := b.resolve(volumeID, rel)
	if err != nil {
		return nil, 0, err
	}
	f, err := os.Open(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, domain.ErrNotFound
		}
		return nil, 0, err
	}
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		_ = f.Close()
		if err != nil {
			return nil, 0, err
		}
		return nil, 0, domain.ErrInvalidInput
	}
	return f, st.Size(), nil
}

func (b *Browser) Put(_ context.Context, volumeID, rel string, r io.Reader) (int64, error) {
	if r == nil || strings.TrimSpace(rel) == "" {
		return 0, domain.ErrInvalidInput
	}
	abs, err := b.resolve(volumeID, rel)
	if err != nil {
		return 0, err
	}
	v, ok := b.find(volumeID)
	if !ok || abs == v.abs {
		return 0, domain.ErrInvalidInput
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return 0, err
	}
	f, err := os.OpenFile(abs, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(f, r)
	closeErr := f.Close()
	if err != nil {
		_ = os.Remove(abs)
		return n, err
	}
	if closeErr != nil {
		return n, closeErr
	}
	return n, nil
}

func (b *Browser) Mkdir(_ context.Context, volumeID, rel, name string) error {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return domain.ErrInvalidInput
	}
	parent, err := b.resolve(volumeID, rel)
	if err != nil {
		return err
	}
	return os.Mkdir(filepath.Join(parent, name), 0o755)
}

func (b *Browser) Remove(_ context.Context, volumeID, rel string) error {
	if strings.TrimSpace(rel) == "" {
		return domain.ErrInvalidInput
	}
	abs, err := b.resolve(volumeID, rel)
	if err != nil {
		return err
	}
	v, ok := b.find(volumeID)
	if !ok || abs == v.abs {
		return domain.ErrInvalidInput
	}
	if err := os.RemoveAll(abs); err != nil {
		if os.IsNotExist(err) {
			return domain.ErrNotFound
		}
		return err
	}
	return nil
}

func (b *Browser) resolve(volumeID, rel string) (string, error) {
	v, ok := b.find(volumeID)
	if !ok || v.abs == "" {
		return "", domain.ErrNotFound
	}
	rel = strings.Trim(strings.ReplaceAll(rel, "\\", "/"), "/")
	if rel == "" {
		return v.abs, nil
	}
	for _, p := range strings.Split(rel, "/") {
		if p == "" || p == "." || p == ".." {
			return "", domain.ErrInvalidInput
		}
	}
	abs := filepath.Join(v.abs, filepath.FromSlash(rel))
	if !within(abs, v.abs) {
		return "", domain.ErrInvalidInput
	}
	return abs, nil
}

func within(abs, root string) bool {
	abs, err1 := filepath.Abs(abs)
	root, err2 := filepath.Abs(root)
	if err1 != nil || err2 != nil {
		return false
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func isDir(info fs.FileInfo) bool {
	return info.Mode()&os.ModeDir != 0
}

func loadExtra(path string) []volume {
	if path == "" {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var rows []extraRoot
	if json.Unmarshal(raw, &rows) != nil {
		return nil
	}
	out := make([]volume, 0, len(rows))
	for _, r := range rows {
		p := filepath.Clean(strings.TrimSpace(r.Path))
		if !filepath.IsAbs(p) {
			continue
		}
		st, err := os.Stat(p)
		if err != nil || !st.IsDir() {
			continue
		}
		id := strings.TrimSpace(r.ID)
		if id == "" {
			id = safeID("saf-", filepath.Base(p))
		}
		if strings.ContainsAny(id, `/\`) {
			continue
		}
		label := strings.TrimSpace(r.Label)
		if label == "" {
			label = filepath.Base(p)
		}
		out = append(out, volume{id: id, label: label, abs: p})
	}
	return out
}

func safeID(prefix, name string) string {
	var b strings.Builder
	b.WriteString(prefix)
	for _, r := range strings.ToLower(name) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else if b.Len() > len(prefix) {
			b.WriteByte('-')
		}
	}
	if b.Len() == len(prefix) {
		b.WriteString("disk")
	}
	return b.String()
}
