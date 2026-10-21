package httpapi

import (
	"errors"
	"io"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	"lanshare/internal/domain"
	"lanshare/internal/usecase/transfer"
)

var _ transfer.AppendBody = (*appendBody)(nil)

// pastDeadline is a read deadline that has already expired. Setting it wakes
// a Read that is blocked on the connection and makes every later Read on that
// request fail at once.
var pastDeadline = time.Unix(1, 0)

// appendBody is the request body of one append, as the transfer use case sees
// it (transfer.AppendBody). Cancelling a context does not wake a blocked read
// on a network connection, so Interrupt moves the connection's read deadline
// into the past instead. This is done per request through the
// ResponseController, never as a server-wide ReadTimeout, which would break
// large uploads. net/http arms a fresh read deadline when it reads the next
// request on the same connection, so the expired one does not leak into it.
//
// Invariant: once Interrupt has been called, Read never returns file bytes
// again and reports domain.ErrInterrupted for an interruption of its own. A
// deadline error that Interrupt did not cause (none is set by this server) is
// passed through untouched.
type appendBody struct {
	r       io.Reader
	rc      *http.ResponseController
	stopped atomic.Bool
}

// newAppendBody wraps body, the r.Body of the request that w answers.
func newAppendBody(w http.ResponseWriter, body io.Reader) *appendBody {
	return &appendBody{r: body, rc: http.NewResponseController(w)}
}

// Read reads from the request body. After Interrupt it fails without touching
// the body; a read that Interrupt cut short fails with domain.ErrInterrupted.
func (b *appendBody) Read(p []byte) (int, error) {
	if b.stopped.Load() {
		return 0, domain.ErrInterrupted
	}
	n, err := b.r.Read(p)
	if err != nil && b.stopped.Load() && errors.Is(err, os.ErrDeadlineExceeded) {
		return n, domain.ErrInterrupted
	}
	return n, err
}

// Interrupt makes a blocked Read, and every later one, return at once. It is
// safe to call from any goroutine and more than once.
func (b *appendBody) Interrupt() {
	// The flag goes first so the Read that the deadline wakes sees it.
	b.stopped.Store(true)
	// Not every ResponseWriter supports deadlines (a test recorder does not).
	// Read still stops at its next call because of the flag.
	_ = b.rc.SetReadDeadline(pastDeadline)
}
