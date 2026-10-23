//go:build windows

package caffeinate

import (
	"context"
	"fmt"
	"sync"
	"syscall"

	"lanshare/internal/usecase/port"
)

// SetThreadExecutionState flags (winbase.h). The assertion is held on one
// dedicated goroutine because the API is per-thread.
const (
	esContinuous       = 0x80000000
	esSystemRequired   = 0x00000001
	esAwaymodeRequired = 0x00000040
)

var procSetThreadExecutionState = syscall.NewLazyDLL("kernel32.dll").NewProc("SetThreadExecutionState")

// Launcher starts a Windows sleep-prevention assertion.
type Launcher struct{}

// NewLauncher returns a Launcher.
func NewLauncher() *Launcher { return &Launcher{} }

var _ port.CaffeinateLauncher = (*Launcher)(nil)

// Start holds a system-required execution state until Stop. pid is accepted
// to match the port (macOS binds caffeinate to it); Windows releases the
// assertion automatically if this process dies.
func (Launcher) Start(pid int) (port.CaffeinateProcess, error) {
	if pid <= 0 {
		return nil, fmt.Errorf("keep-awake: invalid pid %d", pid)
	}
	p := &process{
		stop:   make(chan struct{}),
		exited: make(chan error, 1),
		done:   make(chan struct{}),
	}
	started := make(chan error, 1)
	go p.hold(started)
	if err := <-started; err != nil {
		return nil, err
	}
	return p, nil
}

type process struct {
	stopOnce sync.Once
	stop     chan struct{}
	exited   chan error
	done     chan struct{}
}

func (p *process) hold(started chan<- error) {
	flags := uint32(esContinuous | esSystemRequired | esAwaymodeRequired)
	if err := setExecState(flags); err != nil {
		flags = esContinuous | esSystemRequired
		if err := setExecState(flags); err != nil {
			started <- err
			close(p.done)
			return
		}
	}
	started <- nil
	<-p.stop
	_ = setExecState(esContinuous)
	close(p.done)
}

func (p *process) Exited() <-chan error { return p.exited }

func (p *process) Stop(ctx context.Context) error {
	select {
	case <-p.done:
		return nil
	default:
	}
	p.stopOnce.Do(func() { close(p.stop) })
	select {
	case <-p.done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("wait for keep-awake to release: %w", ctx.Err())
	}
}

func setExecState(flags uint32) error {
	r, _, e := procSetThreadExecutionState.Call(uintptr(flags))
	if r != 0 {
		return nil
	}
	if e != nil && e != syscall.Errno(0) {
		return fmt.Errorf("SetThreadExecutionState: %w", e)
	}
	return fmt.Errorf("SetThreadExecutionState failed")
}
