//go:build tinygo || force_tinygo_logic

package https

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"time"
)

// stdTransport is unused on the native path; it keeps the Transport struct
// identical across build configurations.
type stdTransport struct{}

// roundTrip runs one request, over a pooled connection when one is available.
//
// A connection taken from the pool may already have been closed by the peer,
// and nothing short of using it can reveal that. The loop exists for exactly
// that case: a reused connection that fails before any response byte arrives is
// retried once on a fresh one, which is indistinguishable from having dialed in
// the first place.
func (t *Transport) roundTrip(req *http.Request) (*http.Response, error) {
	secure := req.URL.Scheme == "https"
	host, port := hostPort(req.URL)

	key, viaProxy, err := connKey(req.URL.Scheme, host, port, secure)
	if err != nil {
		closeRequestBody(req)
		return nil, &Error{Op: "dial", Host: host, Backend: backendName, Err: err}
	}

	for attempt := 0; ; attempt++ {
		// On a retry the original body is already spent, so it is rebuilt from
		// GetBody. A nil body here means "use the one on the request".
		body, err := attemptBody(req, attempt)
		if err != nil {
			return nil, &Error{Op: "write", Host: host, Backend: backendName, Err: err}
		}

		pc := t.lease(key)
		reused := pc != nil
		if !reused {
			conn, err := t.dial(req.Context(), secure, host, port)
			if err != nil {
				if body != nil {
					body.Close()
				} else {
					closeRequestBody(req)
				}
				return nil, err
			}
			pc = newPersistConn(conn, key)
		}

		resp, err := t.exchange(req, body, pc, host, viaProxy)
		if err == nil {
			return resp, nil
		}
		if attempt == 0 && reused && pc.untouched() && replayable(req) && notCancelled(req) {
			continue
		}
		return nil, err
	}
}

// dial opens a connection to the origin, through a proxy when the environment
// names one.
func (t *Transport) dial(ctx context.Context, secure bool, host, port string) (net.Conn, error) {
	if secure {
		return dialTLSMaybeProxy(ctx, host, port, t.Config, t.dialTimeout())
	}
	// http:// is reachable because a redirect from https may land there. The
	// proxied form is already reflected in the pool key.
	conn, _, err := dialPlainMaybeProxy(ctx, host, port, t.dialTimeout())
	return conn, err
}

func (t *Transport) exchange(req *http.Request, body io.ReadCloser, pc *persistConn, host string, viaProxy bool) (*http.Response, error) {
	pc.begin()

	if err := pc.conn.SetDeadline(t.deadlineFor(req)); err != nil {
		// This is the one failure that happens before Write, which is what
		// otherwise closes the body. RoundTrip must close it either way.
		pc.close()
		if body != nil {
			body.Close()
		} else {
			closeRequestBody(req)
		}
		return nil, &Error{Op: "write", Host: host, Backend: backendName, Err: err}
	}

	// A conn deadline alone is not enough. http.Client.Timeout cancels through
	// Request.Cancel for a RoundTripper it does not recognise, and TinyGo's
	// net/http does not always put that deadline on the request context, so a
	// deadline-only implementation lets Client.Timeout be ignored entirely.
	// Watching both signals and closing the connection covers either case.
	stop := watchCancel(req, pc)

	// RoundTrip must not mutate req, hence the clone.
	out := req.Clone(req.Context())
	if body != nil {
		out.Body = body
	}
	if t.DisableKeepAlives {
		out.Close = true
	}
	// An http:// request sent to a proxy takes the absolute form,
	// "GET http://host/path", rather than the origin form. WriteProxy is the
	// only difference; a CONNECT tunnel is transparent and uses Write.
	write := out.Write
	if viaProxy {
		write = out.WriteProxy
	}
	if err := write(pc.conn); err != nil {
		stop()
		pc.close()
		return nil, cancelledOr(req, &Error{Op: "write", Host: host, Backend: backendName, Err: err})
	}

	resp, err := http.ReadResponse(pc.br, req)
	if err != nil {
		stop()
		pc.close()
		return nil, cancelledOr(req, &Error{Op: "read", Host: host, Backend: backendName, Err: err})
	}

	// Closing the body releases the connection, to the pool or to the OS, and
	// stops the watcher so it does not outlive the request.
	resp.Body = &poolBody{
		rc:       resp.Body,
		pc:       pc,
		t:        t,
		stop:     stop,
		reusable: !out.Close && reusableResponse(req, resp),
	}
	return resp, nil
}

// deadlineFor picks the deadline for one exchange. The zero time is meaningful:
// it clears whatever the previous request left on a pooled connection, which
// would otherwise fire as a spurious timeout on this one.
func (t *Transport) deadlineFor(req *http.Request) time.Time {
	if deadline, ok := req.Context().Deadline(); ok {
		return deadline
	}
	if t.ResponseTimeout > 0 {
		return time.Now().Add(t.ResponseTimeout)
	}
	return time.Time{}
}

// attemptBody returns the body to send on this attempt, or nil to send the one
// already on the request. Only a retry needs a rebuilt body.
func attemptBody(req *http.Request, attempt int) (io.ReadCloser, error) {
	if attempt == 0 || req.Body == nil || req.GetBody == nil {
		return nil, nil
	}
	return req.GetBody()
}

// replayable reports whether the request can be sent a second time. A body that
// cannot be rebuilt makes it unrepeatable, whatever the method.
func replayable(req *http.Request) bool {
	return req.Body == nil || req.GetBody != nil
}

// notCancelled reports that the caller still wants the request. Retrying after
// a cancellation would report a connection error for what was a deliberate
// stop.
func notCancelled(req *http.Request) bool {
	if req.Context().Err() != nil {
		return false
	}
	select {
	case <-req.Cancel:
		return false
	default:
		return true
	}
}

func closeRequestBody(req *http.Request) {
	if req.Body != nil {
		req.Body.Close()
	}
}

// watchCancel closes conn when the request context is done or Request.Cancel
// fires. The returned function stops the watcher.
func watchCancel(req *http.Request, pc *persistConn) func() {
	ctx := req.Context()
	cancelCh := req.Cancel
	if ctx.Done() == nil && cancelCh == nil {
		return func() {}
	}

	done := make(chan struct{})
	var once sync.Once
	stop := func() { once.Do(func() { close(done) }) }

	go func() {
		select {
		case <-ctx.Done():
			pc.close()
		case <-cancelCh:
			pc.close()
		case <-done:
		}
	}()
	return stop
}

// cancelledOr replaces a generic I/O failure with the cancellation reason when
// the request was cancelled, so http.Client reports a timeout rather than a
// confusing "connection closed".
func cancelledOr(req *http.Request, err error) error {
	if ctxErr := req.Context().Err(); ctxErr != nil {
		if errors.Is(ctxErr, context.DeadlineExceeded) {
			return &Error{Op: "read", Host: requestHost(req), Backend: backendName, Err: errTimeout}
		}
		return ctxErr
	}
	select {
	case <-req.Cancel:
		return &Error{Op: "read", Host: requestHost(req), Backend: backendName, Err: errTimeout}
	default:
	}
	return err
}

func requestHost(req *http.Request) string {
	if req.URL == nil {
		return ""
	}
	return req.URL.Host
}
