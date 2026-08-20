//go:build tinygo || force_tinygo_logic

package httpserver

import (
	"bufio"
	"bytes"
	"errors"
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
	if cfg.ReadHeaderTimeout > 0 {
		_ = conn.SetReadDeadline(time.Time{})
	}

	// Parsing a Request costs a bufio.Reader, a URL, and a header map, and the
	// ordinary path throws all of it away for net/http to parse the same bytes
	// again. A custom predicate needs the Request, but the default one asks a
	// single question — does Connection carry the upgrade token — which the raw
	// bytes can answer, so the parse waits until a handler will actually see it.
	var req *http.Request
	if !cfg.defaultBypass {
		req, err = http.ReadRequest(bufio.NewReader(bytes.NewReader(head)))
		if err != nil {
			conn.Close()
			return
		}
	}

	bypass := false
	if cfg.defaultBypass {
		bypass = rawHeadHasUpgrade(head)
	} else {
		bypass = cfg.ShouldBypass(req)
	}
	if !bypass {
		// Ordinary traffic: net/http serves this request and every later one on
		// the connection, with the bytes already read put back in front.
		handoff.offer(&replayConn{Conn: conn, head: head})
		return
	}

	if req == nil {
		req, err = http.ReadRequest(bufio.NewReader(bytes.NewReader(head)))
		if err != nil {
			conn.Close()
			return
		}
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
// on the net/http path, and refused on the bypass path. The connection reads
// straight into the head buffer's spare capacity, and only the bytes a read
// just delivered — plus the three before them that could complete a straddling
// terminator — are scanned, so accumulating the head stays linear.
func readHead(conn net.Conn, max int) ([]byte, error) {
	buf := make([]byte, 0, 1024)
	for {
		if len(buf) == cap(buf) {
			grown := make([]byte, len(buf), 2*cap(buf))
			copy(grown, buf)
			buf = grown
		}
		n, err := conn.Read(buf[len(buf):cap(buf)])
		if n > 0 {
			start := len(buf) - (len(headTerminator) - 1)
			if start < 0 {
				start = 0
			}
			buf = buf[:len(buf)+n]
			if bytes.Contains(buf[start:], headTerminator) {
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

// rawHeadHasUpgrade reports whether the header block in head carries a
// Connection field with the "upgrade" token, matching what IsUpgrade would say
// about the parsed request. It scans the bytes in place — no split, no header
// map — because on the default configuration this is the only question the
// demultiplexer needs answered before choosing a path. Obs-folded continuation
// lines count as part of the preceding field joined by one space, which is how
// net/textproto folds them, so a token cut across a fold does not match here
// either.
func rawHeadHasUpgrade(head []byte) bool {
	if i := bytes.Index(head, headTerminator); i >= 0 {
		head = head[:i+2]
	}
	i := bytes.IndexByte(head, '\n')
	if i < 0 {
		return false
	}
	head = head[i+1:]

	var scan upgradeTokenScanner
	inConnection := false
	for len(head) > 0 {
		line := head
		if i := bytes.IndexByte(head, '\n'); i >= 0 {
			line, head = head[:i], head[i+1:]
		} else {
			head = nil
		}
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}
		if len(line) == 0 {
			break
		}
		if line[0] == ' ' || line[0] == '\t' {
			if inConnection {
				scan.space()
				for _, b := range line {
					if scan.feed(b) {
						return true
					}
				}
			}
			continue
		}
		if inConnection && scan.close() {
			return true
		}
		inConnection = false
		colon := bytes.IndexByte(line, ':')
		if colon < 0 {
			continue
		}
		if !asciiEqualFold(line[:colon], "connection") {
			continue
		}
		inConnection = true
		scan.reset()
		for _, b := range line[colon+1:] {
			if scan.feed(b) {
				return true
			}
		}
	}
	return inConnection && scan.close()
}

// upgradeToken is the one Connection token the default predicate looks for.
const upgradeToken = "upgrade"

// upgradeTokenScanner recognises the upgrade token inside a comma-separated
// list fed one byte at a time. Whitespace around a token is trimmed; any
// whitespace inside one — including the space an obs-fold contributes —
// disqualifies it, exactly as TrimSpace-then-EqualFold treats the parsed value.
type upgradeTokenScanner struct {
	matched  int
	valid    bool
	started  bool
	trailing bool
}

func (s *upgradeTokenScanner) reset() {
	*s = upgradeTokenScanner{valid: true}
}

// space records a whitespace byte, which is leading (ignored) or trailing
// until more content proves it was internal.
func (s *upgradeTokenScanner) space() {
	if s.started {
		s.trailing = true
	}
}

// feed consumes one byte of the value and reports whether a comma just closed
// a token that matched.
func (s *upgradeTokenScanner) feed(b byte) bool {
	switch b {
	case ' ', '\t', '\v', '\f', '\r', '\n':
		s.space()
		return false
	case ',':
		hit := s.close()
		s.reset()
		return hit
	}
	if s.trailing {
		s.valid = false
	}
	s.started = true
	if s.valid {
		if s.matched < len(upgradeToken) && lowerASCII(b) == upgradeToken[s.matched] {
			s.matched++
		} else {
			s.valid = false
		}
	}
	return false
}

// close reports whether the value ended on a matching token.
func (s *upgradeTokenScanner) close() bool {
	return s.valid && s.matched == len(upgradeToken)
}

func lowerASCII(b byte) byte {
	if 'A' <= b && b <= 'Z' {
		return b + ('a' - 'A')
	}
	return b
}

// asciiEqualFold reports whether b equals lower, ignoring ASCII case. lower
// must already be lowercase.
func asciiEqualFold(b []byte, lower string) bool {
	if len(b) != len(lower) {
		return false
	}
	for i := 0; i < len(b); i++ {
		if lowerASCII(b[i]) != lower[i] {
			return false
		}
	}
	return true
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

// replayConn puts already-read bytes back in front of the connection. It
// serves the head itself rather than holding an io.MultiReader, so once the
// head drains every later read costs one nil check instead of two interface
// hops for the life of the connection.
type replayConn struct {
	net.Conn
	head []byte
}

func (c *replayConn) Read(p []byte) (int, error) {
	if len(c.head) > 0 {
		n := copy(p, c.head)
		c.head = c.head[n:]
		if len(c.head) == 0 {
			c.head = nil
		}
		return n, nil
	}
	return c.Conn.Read(p)
}

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
