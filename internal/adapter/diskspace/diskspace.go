// Package diskspace implements port.DiskSpace by asking the operating system
// how much room is left on the drive that holds the data directory.
//
// Only macOS and Linux are supported; on other systems Free always fails so
// callers report the free space as unknown instead of guessing.
package diskspace

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

	"lanshare/internal/usecase/port"
)

var _ port.DiskSpace = (*Checker)(nil)

// Checker reports the writable free space of the drive holding a directory.
//
// The directory may not exist yet (the data directory is created later than
// the checker is wired), so Free measures the nearest existing ancestor. That
// ancestor is on the same drive as the directory will be, unless the
// directory is created as a mount point, which this tool never does.
type Checker struct {
	dir string
}

// New returns a Checker for the drive that holds dir.
func New(dir string) *Checker {
	return &Checker{dir: dir}
}

// Free returns the bytes an unprivileged user can still write: available
// blocks times block size, not the raw free blocks that include space
// reserved for root.
func (c *Checker) Free(ctx context.Context) (uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, fmt.Errorf("diskspace: %w", err)
	}
	p := filepath.Clean(c.dir)
	for {
		free, err := freeBytes(p)
		if err == nil {
			return free, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return 0, fmt.Errorf("diskspace: statfs %q: %w", p, err)
		}
		parent := filepath.Dir(p)
		if parent == p {
			return 0, fmt.Errorf("diskspace: statfs %q: %w", c.dir, err)
		}
		p = parent
	}
}
