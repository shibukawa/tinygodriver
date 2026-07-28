//go:build tinygo || force_tinygo_logic

package https

import (
	"context"
	"net"
	"sync"
	"time"
)

// defaultOpTimeout bounds a single read or write when no deadline is set, so a
// stalled peer cannot block a goroutine forever.
const defaultOpTimeout = 5 * time.Minute

// effectiveTimeout folds a context deadline into the caller's timeout.
//
// The native backends take a duration rather than a context, because the
// handshake happens inside C and there is nothing to cancel once it starts.
// Every entry point that accepts a context therefore narrows its timeout here
// first, so dial and upgrade bound themselves the same way on every platform.
func effectiveTimeout(ctx context.Context, timeout time.Duration) time.Duration {
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining < timeout {
			return remaining
		}
	}
	return timeout
}

// timeoutNanos converts a deadline into the budget a single native call gets.
// It reports false once the deadline has passed.
func timeoutNanos(deadline time.Time) (int64, bool) {
	if deadline.IsZero() {
		return int64(defaultOpTimeout), true
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return 0, false
	}
	return remaining.Nanoseconds(), true
}

// connState holds the deadlines and closed flag every native conn keeps.
//
// Its mutex is deliberately separate from whatever lock a conn uses to
// serialize I/O against its session. SetDeadline must never wait for an
// in-flight Read: callers set a deadline precisely when a read is blocked and
// they want it bounded, so a single lock covering both would make SetDeadline
// block until the read it was meant to cut short finished on its own.
//
// That is not theoretical. A PostgreSQL cancellation watcher sets a deadline
// and then opens a second connection to send CancelRequest; with one shared
// lock the watcher stalls behind the very query it is cancelling, and the
// cancellation silently does nothing.
type connState struct {
	mu            sync.Mutex
	readDeadline  time.Time
	writeDeadline time.Time
	closed        bool
}

// readBudget reports how long a read may block. It returns net.ErrClosed on a
// closed conn and errTimeout once the read deadline has passed.
func (s *connState) readBudget() (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, net.ErrClosed
	}
	ns, ok := timeoutNanos(s.readDeadline)
	if !ok {
		return 0, errTimeout
	}
	return ns, nil
}

// writeBudget is readBudget for the write side.
func (s *connState) writeBudget() (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, net.ErrClosed
	}
	ns, ok := timeoutNanos(s.writeDeadline)
	if !ok {
		return 0, errTimeout
	}
	return ns, nil
}

// deadlines returns the raw deadlines, for backends that pass a time rather
// than a duration to the layer below.
func (s *connState) deadlines() (read, write time.Time, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return time.Time{}, time.Time{}, net.ErrClosed
	}
	return s.readDeadline, s.writeDeadline, nil
}

// close marks the conn closed, reporting false if it already was.
func (s *connState) close() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.closed = true
	return true
}

func (s *connState) setDeadline(t time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.readDeadline, s.writeDeadline = t, t
	return nil
}

func (s *connState) setReadDeadline(t time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.readDeadline = t
	return nil
}

func (s *connState) setWriteDeadline(t time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writeDeadline = t
	return nil
}

// stateError wraps a budget failure. net.ErrClosed passes through untouched so
// callers can still match errors.Is(err, net.ErrClosed); a timeout becomes an
// *Error, which reports Timeout() like the rest of the package.
func stateError(op, host, backend string, err error) error {
	if err == net.ErrClosed {
		return err
	}
	return &Error{Op: op, Host: host, Backend: backend, Err: err}
}
