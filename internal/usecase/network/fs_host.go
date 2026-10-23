package network

import (
	"context"
	"os"
	"path"
	"strings"

	"lanshare/internal/domain"
)

func (s *Service) hostHasVolumes(ctx context.Context) bool {
	if s.host == nil {
		return false
	}
	vols, err := s.host.Volumes(ctx)
	return err == nil && len(vols) > 0
}

func (s *Service) listHost(ctx context.Context, out CableFSList, rootID, rel string) (CableFSList, error) {
	if s.host == nil {
		return CableFSList{}, domain.ErrNotFound
	}
	if rootID == "" {
		vols, err := s.host.Volumes(ctx)
		if err != nil {
			return CableFSList{}, err
		}
		out.Title = out.Name
		for _, v := range vols {
			out.Entries = append(out.Entries, CableFSEntry{
				Name: v.Label, Path: childCablePath(domain.CableHost, v.ID), Dir: true, Kind: "folder",
			})
		}
		return out, nil
	}
	if !s.hostVolume(ctx, rootID) {
		return CableFSList{}, domain.ErrNotFound
	}
	if rel == "" {
		if vols, err := s.host.Volumes(ctx); err == nil {
			for _, v := range vols {
				if v.ID == rootID && v.Label != "" {
					out.Title = v.Label
					break
				}
			}
		}
	}
	ents, err := s.host.List(ctx, rootID, rel)
	if err != nil {
		out.Hint = "อ่านโฟลเดอร์นี้จากเครื่องไม่ได้"
		return out, nil
	}
	if len(ents) > maxCableFSEntries {
		ents = ents[:maxCableFSEntries]
		out.Hint = "แสดงได้สูงสุด 400 รายการ"
	}
	for _, e := range ents {
		kind := "folder"
		if !e.Dir {
			kind = string(domain.KindOf(e.Name))
		}
		out.Entries = append(out.Entries, CableFSEntry{
			Name: e.Name,
			Path: childCablePath(out.Path, e.Name),
			Dir:  e.Dir,
			Size: e.Size,
			Kind: kind,
		})
	}
	return out, nil
}

func (s *Service) hostVolume(ctx context.Context, id string) bool {
	if s.host == nil {
		return false
	}
	vols, err := s.host.Volumes(ctx)
	if err != nil {
		return false
	}
	for _, v := range vols {
		if v.ID == id {
			return true
		}
	}
	return false
}

func (s *Service) openHost(ctx context.Context, rootID, rel string) (CableFile, error) {
	if s.host == nil || strings.TrimSpace(rel) == "" {
		return CableFile{}, domain.ErrInvalidInput
	}
	f, size, err := s.host.Open(ctx, rootID, rel)
	if err != nil {
		return CableFile{}, err
	}
	return CableFile{Name: path.Base(rel), Size: size, Content: f}, nil
}

func (s *Service) mkdirHost(ctx context.Context, rootID, rel, name string) (string, error) {
	if s.host == nil {
		return "", domain.ErrNotFound
	}
	if err := s.host.Mkdir(ctx, rootID, rel, name); err != nil {
		return "", err
	}
	parent := childCablePath(domain.CableHost, rootID)
	if rel != "" {
		parent = childCablePath(parent, rel)
	}
	return childCablePath(parent, name), nil
}

func (s *Service) deleteHost(ctx context.Context, rootID, rel string) error {
	if s.host == nil || strings.TrimSpace(rel) == "" {
		return domain.ErrInvalidInput
	}
	return s.host.Remove(ctx, rootID, rel)
}

func (s *Service) pushHostFiles(ctx context.Context, rootID, rel string, files []cableClipFile) (int, error) {
	if s.host == nil {
		return 0, domain.ErrNotFound
	}
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
		src, err := os.Open(f.Path)
		if err != nil {
			if first == nil {
				first = err
			}
			continue
		}
		_, err = s.host.Put(ctx, rootID, remoteRel, src)
		_ = src.Close()
		if err != nil {
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
