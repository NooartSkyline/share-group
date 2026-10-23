//go:build !darwin && !windows

package caffeinate

import (
	"fmt"

	"lanshare/internal/usecase/port"
)

// Launcher is a no-op keep-awake on operating systems this host does not run.
type Launcher struct{}

// NewLauncher returns a Launcher.
func NewLauncher() *Launcher { return &Launcher{} }

var _ port.CaffeinateLauncher = (*Launcher)(nil)

// Start always fails so the power guard reports unavailable instead of guessing.
func (Launcher) Start(int) (port.CaffeinateProcess, error) {
	return nil, fmt.Errorf("keep-awake: unsupported operating system")
}
