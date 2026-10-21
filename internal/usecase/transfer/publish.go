package transfer

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"lanshare/internal/domain"
	"lanshare/internal/usecase/port"
)

// publishTarget is where a new file goes once its name conflicts are resolved.
type publishTarget struct {
	// Name is the final file name inside the folder.
	Name string
	// Replace is the existing file that an overwrite supersedes, or nil.
	Replace *domain.File
}

// resolveTarget decides the final name of a file arriving in dir, against the
// files the room holds now. Folder and name are compared case-insensitively.
//
// With ConflictOverwrite the name is kept and the file of the same folder and
// name, if any, is returned as Replace; actor must then be the room owner or
// the device that uploaded that file, else domain.ErrForbidden. With any other
// policy (rename is the default) a taken name becomes "name (2).ext" and so on.
//
// The caller must hold roomLock(room) from the moment it lists existing files
// until the new file is saved, or two publishers could pick the same name.
func resolveTarget(existing []domain.File, actor domain.Device, dir, name string, policy ConflictPolicy) (publishTarget, error) {
	if policy == ConflictOverwrite {
		for i := range existing {
			f := existing[i]
			if !sameFolder(f, dir) || !strings.EqualFold(f.Name, name) {
				continue
			}
			if !actor.Role.CanManage() && f.UploaderID != actor.ID {
				return publishTarget{}, domain.ErrForbidden
			}
			return publishTarget{Name: name, Replace: &f}, nil
		}
		return publishTarget{Name: name}, nil
	}
	unique := domain.UniqueName(name, func(c string) bool {
		for _, f := range existing {
			if sameFolder(f, dir) && strings.EqualFold(f.Name, c) {
				return true
			}
		}
		return false
	})
	return publishTarget{Name: unique}, nil
}

// publishPartial turns the finished partial file of session sid into the file
// f of its room, and tells the room. The caller holds roomLock(f.RoomID).
//
// Order: Promote the content, Save the metadata, then (overwrite) delete the
// replaced file, which publishes file.deleted, and only then publish
// file.added. Nothing is published unless the metadata was saved.
//
// If Save fails the content is moved back to the partial (Demote) so the work
// is not lost, and the replaced file was never touched. restored reports
// whether the partial is back in place; it is false only when Demote failed
// as well, in which case the content is deleted so no file content is left
// without metadata, and the session cannot be completed any more. A failed
// Promote leaves the partial where it was (restored is true).
func (s *Service) publishPartial(ctx context.Context, sid domain.SessionID, f domain.File, replace *domain.File) (restored bool, err error) {
	if err := s.upDeps.Partials.Promote(ctx, f.RoomID, sid, f.ID); err != nil {
		return true, fmt.Errorf("move partial file into place: %w", err)
	}
	if err := s.files.Save(ctx, f); err != nil {
		if derr := s.upDeps.Partials.Demote(ctx, f.RoomID, f.ID, sid); derr != nil {
			slog.Warn("cannot move a finished upload back after saving its metadata failed", "error", derr)
			_ = s.store.Delete(ctx, f.RoomID, f.ID)
			return false, fmt.Errorf("save file metadata: %w", err)
		}
		return true, fmt.Errorf("save file metadata: %w", err)
	}
	if replace != nil {
		s.remove(ctx, f.RoomID, replace.ID) // publishes file.deleted
	}
	s.events.Publish(port.Event{Type: port.EventFileAdded, RoomID: f.RoomID, Data: f.ID})
	return true, nil
}

// publishStored makes content that is already in the file store (put there by
// a one-shot upload) the file f of its room, and tells the room. It takes
// roomLock itself.
//
// Inside the lock it resolves the final name against the files of that moment
// (an overwrite is permitted again for actor), saves the metadata, deletes a
// replaced file (publishing file.deleted) and then publishes file.added, in
// that order. f.Name is the sanitized name before conflict resolution.
//
// On any failure the stored content is deleted, so no content is left without
// metadata, a replaced file is untouched and nothing is published.
func (s *Service) publishStored(ctx context.Context, actor domain.Device, f domain.File, policy ConflictPolicy) (domain.File, error) {
	mu := s.roomLock(f.RoomID)
	mu.Lock()
	defer mu.Unlock()

	fail := func(err error) (domain.File, error) {
		_ = s.store.Delete(ctx, f.RoomID, f.ID)
		return domain.File{}, err
	}
	existing, err := s.files.ListByRoom(ctx, f.RoomID)
	if err != nil {
		return fail(fmt.Errorf("list files: %w", err))
	}
	target, err := resolveTarget(existing, actor, f.Dir, f.Name, policy)
	if err != nil {
		return fail(err)
	}
	f.Name = target.Name
	if err := s.files.Save(ctx, f); err != nil {
		return fail(fmt.Errorf("save file metadata: %w", err))
	}
	if target.Replace != nil {
		s.remove(ctx, f.RoomID, target.Replace.ID) // publishes file.deleted
	}
	s.events.Publish(port.Event{Type: port.EventFileAdded, RoomID: f.RoomID, Data: f.ID})
	return f, nil
}
