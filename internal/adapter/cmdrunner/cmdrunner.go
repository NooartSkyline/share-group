// Package cmdrunner runs external diagnostic commands for the server.
//
// Invariants: the command is always started directly from a fixed argv and
// never through a shell; the process is killed when the caller's context
// ends; only a bounded amount of stdout is kept and stderr is discarded.
package cmdrunner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"time"

	"lanshare/internal/usecase/port"
)

// MaxOutput is the most stdout, in bytes, that Run returns. Anything beyond
// it is read and dropped so the child never blocks on a full pipe.
const MaxOutput = 1 << 20 // 1 MiB

// waitDelay bounds how long Run waits for the output pipes to close after the
// process has exited or been killed (for example when a grandchild still holds
// the pipe open).
const waitDelay = time.Second

// Runner implements port.CommandRunner with os/exec.
type Runner struct{}

var _ port.CommandRunner = (*Runner)(nil)

// New returns a Runner.
func New() *Runner { return &Runner{} }

// Run executes name with args and returns its stdout, capped at MaxOutput.
//
// Errors can be told apart with errors.Is / errors.As:
//   - the program does not exist: errors.Is(err, exec.ErrNotFound)
//   - it exited non-zero: errors.As(err, new(*exec.ExitError))
//   - ctx expired or was cancelled (the process is killed):
//     errors.Is(err, context.DeadlineExceeded) / context.Canceled
//
// When ctx has ended it takes precedence over the exit error caused by the
// kill. The error text carries only the program name, never its arguments or
// output.
func (*Runner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out := &limitedBuffer{limit: MaxOutput}
	cmd.Stdout = out // Stdin and Stderr stay nil: /dev/null.
	cmd.WaitDelay = waitDelay

	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("run %s: %w", name, ctxErr)
		}
		if errors.Is(err, exec.ErrNotFound) {
			return nil, fmt.Errorf("run %s: %w", name, err)
		}
		// An absolute path that does not exist fails in fork/exec with
		// ENOENT rather than in LookPath; report it the same way.
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("run %s: %w: %w", name, exec.ErrNotFound, err)
		}
		return nil, fmt.Errorf("run %s: %w", name, err)
	}
	return out.buf.Bytes(), nil
}

// limitedBuffer keeps the first limit bytes written and silently discards the
// rest while still reporting every write as accepted.
type limitedBuffer struct {
	buf   bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if room := b.limit - b.buf.Len(); room > 0 {
		if len(p) < room {
			room = len(p)
		}
		b.buf.Write(p[:room])
	}
	return len(p), nil
}
