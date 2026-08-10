//go:build tinygo || force_tinygo_logic

package httpserver

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net"
	"net/http"
	"time"
)

// Backend identifies the implementation selected by build constraints.
const Backend = "tinygo"

// headTerminator ends the request head. A request that never sends it is a
// request this package never dispatches.
var headTerminator = []byte("\r\n\r\n")

// serve reads each connection's first request head, then either hands the
// connection to net/http with the head replayed, or calls the handler over a
// ResponseWriter it can hijack. See the package doc for why net/http cannot be
// left to do this itself.
func serve(ln net.Listener, srv *http.Server, cfg Config) error {
	h := handlerOf(srv)

	// Guard only what net/http sees. The bypass path calls h directly, so an
	// upgrade never meets this wrapper on the path that can serve it. Assigning
	// to srv rather than to a copy keeps srv.Shutdown in charge, and happens
	// before the listener is read.
	srv.Handler = guardUpgrades(h, cfg.ShouldBypass)

	handoff := newChanListener(ln.Addr())
	served := make(chan error, 1)
	go func() { served <- srv.Serve(handoff) }()

	for {
		conn, err := ln.Accept()
		if err != nil {
			// Let net/http finish the connections it already has, then report
			// what it reports: callers match on http.ErrServerClosed.
			handoff.close()
			if inner := <-served; inner != nil {
				return inner
			}
			return err
		}
		go dispatch(conn, h, cfg, handoff, maxHeaderBytes(srv))
	}
}

// dispatch routes one connection. It runs on its own goroutine because reading
// the head blocks.
func dispatch(conn net.Conn, h http.Handler, cfg Config, handoff *chanListener, maxHead int) {
	// The deadline is set before the read begins, which is the only kind netdev
	// honours: one set afterwards cannot reach a read already in flight. That
	// limitation is the whole reason this package exists.
	if cfg.ReadHeaderTimeout > 0 {
		_ = conn.SetReadDeadline(time.Now().Add(cfg.ReadHeaderTimeout))
	}
	head, err := readHead(conn, maxHead)
	if err != nil {
		conn.Close()
		return
	}
	req, err := http.ReadRequest(bufio.NewReader(bytes.NewReader(head)))
	if err != nil {
		conn.Close()
		return
	}
	if cfg.ReadHeaderTimeout > 0 {
		_ = conn.SetReadDeadline(time.Time{})
	}

	if !cfg.ShouldBypass(req) {
		// Ordinary traffic: net/http serves this request and every later one on
		// the connection, with the bytes already read put back in front.
		handoff.offer(&replayConn{Conn: conn, r: io.MultiReader(bytes.NewReader(head), conn)})
		return
	}

	// An upgrade carries no body and no early data, and a client that sends
	// some would lose it here: the hijacked reader starts at the connection,
	// not at these leftover bytes. Refuse instead of dropping them silently.
	if idx := bytes.Index(head, headTerminator); idx >= 0 && idx+len(headTerminator) < len(head) {
		writeSimple(conn, http.StatusBadRequest, "client sent data before the upgrade completed")
		conn.Close()
		return
	}

	req.RemoteAddr = conn.RemoteAddr().String()
	w := &bypassWriter{
		conn: conn,
		br:   bufio.NewReader(conn),
		bw:   bufio.NewWriter(conn),
		hdr:  make(http.Header),
	}
	h.ServeHTTP(w, req)
	if w.hijacked {
		// The handler owns the connection now, including closing it.
		return
	}
	w.finish()
	conn.Close()
}

// guardUpgrades answers 501 to an upgrade that reached net/http, which happens
// only when one arrives as a later request on a reused connection. Attempting
// it would hang; this makes the limit visible instead.
func guardUpgrades(h http.Handler, shouldBypass func(*http.Request) bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if shouldBypass(r) {
			http.Error(w,
				"httpserver: protocol upgrade on a reused connection is not supported on this platform",
				http.StatusNotImplemented)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// errHeadTooLarge reports a head that never ended within the limit.
var errHeadTooLarge = errors.New("httpserver: request head too large")

// readHead reads until the end of the header block. It reads in chunks and so
// may take in a few bytes beyond the terminator; every one of them is replayed
// on the net/http path, and refused on the bypass path.
func readHead(conn net.Conn, max int) ([]byte, error) {
	buf := make([]byte, 0, 1024)
	tmp := make([]byte, 512)
	for {
		n, err := conn.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			if bytes.Contains(buf, headTerminator) {
				return buf, nil
			}
			if len(buf) > max {
				return nil, errHeadTooLarge
			}
		}
		if err != nil {
			return nil, err
		}
	}
}

// maxHeaderBytes mirrors net/http's own resolution of the limit.
func maxHeaderBytes(srv *http.Server) int {
	if srv.MaxHeaderBytes > 0 {
		return srv.MaxHeaderBytes
	}
	return http.DefaultMaxHeaderBytes
}

// writeSimple emits a minimal response for the errors dispatch reports before
// any handler runs.
func writeSimple(conn net.Conn, code int, msg string) {
	bw := bufio.NewWriter(conn)
	writeStatusLine(bw, code)
	writeHeaders(bw, len(msg), nil)
	bw.WriteString(msg)
	bw.Flush()
}

// replayConn puts already-read bytes back in front of the connection.
type replayConn struct {
	net.Conn
	r io.Reader
}

func (c *replayConn) Read(p []byte) (int, error) { return c.r.Read(p) }

// chanListener feeds net/http the connections the demultiplexer judged to be
// ordinary HTTP. Close makes Accept return, which is how http.Server learns the
// real listener is done.
type chanListener struct {
	conns  chan net.Conn
	done   chan struct{}
	closed chan struct{}
	addr   net.Addr
}

func newChanListener(addr net.Addr) *chanListener {
	return &chanListener{
		conns:  make(chan net.Conn),
		done:   make(chan struct{}),
		closed: make(chan struct{}),
		addr:   addr,
	}
}

func (l *chanListener) offer(c net.Conn) {
	select {
	case l.conns <- c:
	case <-l.done:
		c.Close()
	}
}

func (l *chanListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

// Close is idempotent: http.Server calls it during Shutdown, and serve calls it
// when the real listener stops.
func (l *chanListener) Close() error {
	select {
	case <-l.done:
	default:
		close(l.done)
	}
	return nil
}

func (l *chanListener) close()         { l.Close() }
func (l *chanListener) Addr() net.Addr { return l.addr }
