//go:build darwin

// Package caffeinate implements port.CaffeinateLauncher with the macOS
// /usr/bin/caffeinate tool.
//
// The command line is fixed: only the numeric pid varies, and no shell is
// involved. The child runs in its own process group so a terminal Ctrl-C does
// not kill it before the owner stops it deliberately; "-w pid" still makes it
// exit by itself when the server dies abnormally.
package caffeinate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"

	"lanshare/internal/usecase/port"
)

// binary is the absolute path of the system tool. It is never looked up on
// PATH, so a modified PATH cannot substitute another program.
const binary = "/usr/bin/caffeinate"

// Launcher starts caffeinate child processes.
type Launcher struct{}

// NewLauncher returns a Launcher.
func NewLauncher() *Launcher { return &Launcher{} }

var _ port.CaffeinateLauncher = (*Launcher)(nil)

// args builds the fixed argument list: "-i" prevents idle sleep and "-w pid"
// releases the assertion when the process with that pid ends.
func args(pid int) []string {
	return []string{"-i", "-w", strconv.Itoa(pid)}
}

// Start runs caffeinate bound to pid. The returned process is already running;
// its Exited channel reports when it ends for any reason.
func (Launcher) Start(pid int) (port.CaffeinateProcess, error) {
	if pid <= 0 {
		return nil, fmt.Errorf("caffeinate: invalid pid %d", pid)
	}
	cmd := exec.Command(binary, args(pid)...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start caffeinate: %w", err)
	}
	p := &process{
		cmd:    cmd,
		exited: make(chan error, 1),
		done:   make(chan struct{}),
	}
	go p.wait()
	return p, nil
}

// process is a running caffeinate child. Exactly one goroutine calls Wait.
type process struct {
	cmd *exec.Cmd
	// exited gets the Wait result once, then is closed. It is buffered so the
	// waiter never blocks when nobody reads it.
	exited chan error
	// done is closed after Wait returned; Stop waits on it so it works
	// whether or not the owner consumes exited.
	done chan struct{}
}

func (p *process) wait() {
	err := p.cmd.Wait()
	if err != nil {
		err = fmt.Errorf("caffeinate exited: %w", err)
	}
	p.exited <- err
	close(p.exited)
	close(p.done)
}

// Exited implements port.CaffeinateProcess.
func (p *process) Exited() <-chan error { return p.exited }

// Stop sends SIGTERM and waits until the process has exited or ctx ends. If
// ctx ends first the process is killed with SIGKILL and ctx's error is
// returned. Stopping a process that already exited returns nil.
func (p *process) Stop(ctx context.Context) error {
	select {
	case <-p.done:
		return nil
	default:
	}
	if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("signal caffeinate: %w", err)
	}
	select {
	case <-p.done:
		return nil
	case <-ctx.Done():
		// Best effort: do not leave the assertion behind.
		_ = p.cmd.Process.Kill()
		return fmt.Errorf("wait for caffeinate to exit: %w", ctx.Err())
	}
}
