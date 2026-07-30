//go:build tinygo || force_tinygo_logic

package https

import (
	"bufio"
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

// roundTrip dials a TLS connection, writes the request, and parses the
// response. There is no connection reuse: the connection is released when the
// response body is closed.
func (t *Transport) roundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme != "https" {
		return t.roundTripPlain(req)
	}

	host, port := hostPort(req.URL)
	conn, err := dialTLSMaybeProxy(req.Context(), host, port, t.Config, t.dialTimeout())
	if err != nil {
		if req.Body != nil {
			req.Body.Close()
		}
		return nil, err
	}

	// A CONNECT tunnel is transparent once established, so the request is
	// written in origin form exactly as it would be on a direct connection.
	return t.exchange(req, conn, host, false)
}

// roundTripPlain handles http:// so a redirect from https to http still works.
func (t *Transport) roundTripPlain(req *http.Request) (*http.Response, error) {
	host, port := hostPort(req.URL)
	conn, viaProxy, err := dialPlainMaybeProxy(req.Context(), host, port, t.dialTimeout())
	if err != nil {
		if req.Body != nil {
			req.Body.Close()
		}
		return nil, err
	}
	return t.exchange(req, conn, host, viaProxy)
}

func (t *Transport) exchange(req *http.Request, conn net.Conn, host string, viaProxy bool) (*http.Response, error) {
	if deadline, ok := req.Context().Deadline(); ok {
		conn.SetDeadline(deadline)
	} else if t.ResponseTimeout > 0 {
		conn.SetDeadline(time.Now().Add(t.ResponseTimeout))
	}

	// A conn deadline alone is not enough. http.Client.Timeout cancels through
	// Request.Cancel for a RoundTripper it does not recognise, and TinyGo's
	// net/http does not always put that deadline on the request context, so a
	// deadline-only implementation lets Client.Timeout be ignored entirely.
	// Watching both signals and closing the connection covers either case.
	stop := watchCancel(req, conn)

	// There is no connection reuse in v1, so tell the server not to hold the
	// connection open. RoundTrip must not mutate req, hence the clone.
	out := req.Clone(req.Context())
	out.Close = true
	// An http:// request sent to a proxy takes the absolute form,
	// "GET http://host/path", rather than the origin form. WriteProxy is the
	// only difference; a CONNECT tunnel is transparent and uses Write.
	write := out.Write
	if viaProxy {
		write = out.WriteProxy
	}
	if err := write(conn); err != nil {
		stop()
		conn.Close()
		return nil, cancelledOr(req, &Error{Op: "write", Host: host, Backend: backendName, Err: err})
	}

	resp, err := http.ReadResponse(bufio.NewReader(conn), req)
	if err != nil {
		stop()
		conn.Close()
		return nil, cancelledOr(req, &Error{Op: "read", Host: host, Backend: backendName, Err: err})
	}

	// Closing the body must release the connection, and stop the watcher so it
	// does not outlive the request.
	resp.Body = &bodyConn{ReadCloser: resp.Body, conn: conn, stop: stop}
	return resp, nil
}

// watchCancel closes conn when the request context is done or Request.Cancel
// fires. The returned function stops the watcher.
func watchCancel(req *http.Request, conn net.Conn) func() {
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
			conn.Close()
		case <-cancelCh:
			conn.Close()
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
			return &Error{Op: "read", Host: req.URL.Host, Backend: backendName, Err: errTimeout}
		}
		return ctxErr
	}
	select {
	case <-req.Cancel:
		return &Error{Op: "read", Host: req.URL.Host, Backend: backendName, Err: errTimeout}
	default:
	}
	return err
}

// bodyConn closes the underlying connection when the response body is closed.
type bodyConn struct {
	io.ReadCloser
	conn   net.Conn
	stop   func()
	closed bool
}

func (b *bodyConn) Close() error {
	if b.closed {
		return nil
	}
	b.closed = true
	if b.stop != nil {
		b.stop()
	}
	err := b.ReadCloser.Close()
	if cerr := b.conn.Close(); err == nil {
		err = cerr
	}
	return err
}
