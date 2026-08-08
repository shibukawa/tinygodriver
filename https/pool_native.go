//go:build tinygo || force_tinygo_logic

package https

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Connection reuse for the native path.
//
// Standard Go builds get this from net/http.Transport. The native path builds
// its own connections, so without a pool every request pays a full TLS
// handshake. That is affordable for object transfer, where one handshake sits
// in front of a multi-megabyte body, and ruinous for small RPC: measured
// against a service endpoint the handshake was 87-110 ms in front of work that
// takes well under a millisecond.
//
// The pool is deliberately smaller than net/http's. Two constraints shape it:
//
//   - No maintenance goroutine. A background reaper would have to make progress
//     while the request goroutine blocks in a cgo recv(), which is exactly what
//     the netdev scheduler rules forbid. Idle entries are therefore expired when
//     they are next taken out, not on a timer.
//   - No liveness check. None of the native backends can report that a peer
//     closed an idle connection without reading from it, and a speculative read
//     would consume the next response. A connection that turns out to be dead is
//     recovered by retrying the request once instead.

const (
	// defaultMaxIdleConnsPerHost is deliberately small. The workload this exists
	// for is sequential request-response, which needs exactly one connection;
	// the second slot only absorbs modest concurrency.
	defaultMaxIdleConnsPerHost = 2

	// maxIdleConnsTotal bounds the whole pool, so a program talking to many
	// hosts cannot accumulate native TLS handles without limit.
	maxIdleConnsTotal = 32

	// defaultIdleConnTimeout is far below net/http's 90 seconds because a stale
	// entry here costs a failed request and a retry rather than a cheap liveness
	// check. It sits under the 60 second idle timeout common to AWS service
	// endpoints and load balancers.
	defaultIdleConnTimeout = 20 * time.Second

	// A response body the caller closed without reading can still be drained,
	// which keeps the connection reusable. The caps stop that from turning into
	// an unbounded transfer for a body nobody wanted.
	maxDrainBytes = 4 << 10
	maxDrainReads = 64
	drainTimeout  = 250 * time.Millisecond
)

// persistConn is a connection that may outlive the request that opened it.
//
// The bufio.Reader is part of the connection, not part of the request. Response
// parsing reads ahead, so a reader created per request would discard bytes that
// belong to whatever comes next.
type persistConn struct {
	conn net.Conn
	br   *bufio.Reader
	key  string

	// idleSince is set on release and read on lease. Both happen under the
	// pool's lock, so it needs no lock of its own.
	idleSince time.Time

	mu     sync.Mutex
	broken bool

	// nread counts response bytes read during the current request. Atomic
	// rather than under mu: it is bumped on every Read and only inspected
	// between requests.
	nread atomic.Int64
}

func newPersistConn(conn net.Conn, key string) *persistConn {
	pc := &persistConn{conn: conn, key: key}
	pc.br = bufio.NewReader(pc)
	return pc
}

// Read counts what the response parser consumes. The count is the evidence that
// decides whether a failed request may be replayed: once a single response byte
// has arrived the server has acted on the request, and resending it is not the
// transport's call to make.
func (pc *persistConn) Read(p []byte) (int, error) {
	n, err := pc.conn.Read(p)
	if n > 0 {
		pc.nread.Add(int64(n))
	}
	return n, err
}

// begin resets the per-request state. A pooled connection carries none of the
// previous request's accounting into the next one.
func (pc *persistConn) begin() {
	pc.nread.Store(0)
}

// untouched reports that no response byte arrived for the current request.
//
// It deliberately ignores the broken flag. By the time a failed exchange is
// judged for replay the connection has already been closed, so broken says
// nothing about whether the server acted on the request; only the byte count
// does. Cancellation, the other reason a connection gets closed underneath a
// request, is tested separately.
func (pc *persistConn) untouched() bool {
	return pc.nread.Load() == 0
}

// close marks the connection unusable and closes it. It is safe to call from
// the cancellation watcher while the request goroutine is blocked in Read.
func (pc *persistConn) close() error {
	pc.mu.Lock()
	first := !pc.broken
	pc.broken = true
	pc.mu.Unlock()
	if !first {
		return nil
	}
	return pc.conn.Close()
}

func (pc *persistConn) isBroken() bool {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	return pc.broken
}

// connPool holds idle connections keyed by destination. Its zero value is
// usable, which is what lets Transport keep it as a plain field.
type connPool struct {
	mu    sync.Mutex
	idle  map[string][]*persistConn
	total int
}

// get returns a reusable connection for key, or nil. Entries idle for longer
// than timeout are closed on the way past: this is the only moment the pool
// gets to notice they have aged out.
func (p *connPool) get(key string, timeout time.Duration) *persistConn {
	p.mu.Lock()
	defer p.mu.Unlock()

	list := p.idle[key]
	// Newest first. A connection idle for less time is likelier to still be
	// open, and taking from the tail leaves the older ones to expire.
	for len(list) > 0 {
		pc := list[len(list)-1]
		list = list[:len(list)-1]
		p.total--
		if time.Since(pc.idleSince) < timeout && !pc.isBroken() {
			p.store(key, list)
			return pc
		}
		pc.close()
	}
	p.store(key, nil)
	return nil
}

// put files pc as idle, reporting whether the pool took it. A false result
// leaves the connection to the caller to close.
func (p *connPool) put(pc *persistConn, maxPerHost int) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.idle[pc.key]) >= maxPerHost {
		return false
	}
	if p.total >= maxIdleConnsTotal && !p.evictOldest() {
		return false
	}
	pc.idleSince = time.Now()
	if p.idle == nil {
		p.idle = map[string][]*persistConn{}
	}
	p.idle[pc.key] = append(p.idle[pc.key], pc)
	p.total++
	return true
}

// evictOldest makes room by closing the least recently released connection
// anywhere in the pool. It reports false when there was nothing to evict.
func (p *connPool) evictOldest() bool {
	var (
		oldest *persistConn
		from   string
		at     int
	)
	for key, list := range p.idle {
		for i, pc := range list {
			if oldest == nil || pc.idleSince.Before(oldest.idleSince) {
				oldest, from, at = pc, key, i
			}
		}
	}
	if oldest == nil {
		return false
	}
	list := p.idle[from]
	p.store(from, append(list[:at], list[at+1:]...))
	p.total--
	oldest.close()
	return true
}

// store writes back a bucket, dropping the key when the bucket is empty so a
// long-lived Transport does not retain an entry per host it ever contacted.
func (p *connPool) store(key string, list []*persistConn) {
	if len(list) == 0 {
		delete(p.idle, key)
		return
	}
	if p.idle == nil {
		p.idle = map[string][]*persistConn{}
	}
	p.idle[key] = list
}

// closeAll drops every idle connection.
func (p *connPool) closeAll() {
	p.mu.Lock()
	idle := p.idle
	p.idle = nil
	p.total = 0
	p.mu.Unlock()

	for _, list := range idle {
		for _, pc := range list {
			pc.close()
		}
	}
}

// CloseIdleConnections closes connections that are being kept for reuse. It
// mirrors net/http.Transport.CloseIdleConnections and does not affect requests
// still in flight.
func (t *Transport) CloseIdleConnections() {
	t.pool.closeAll()
}

func (t *Transport) maxIdleConnsPerHost() int {
	if t.MaxIdleConnsPerHost <= 0 {
		return defaultMaxIdleConnsPerHost
	}
	return t.MaxIdleConnsPerHost
}

func (t *Transport) idleConnTimeout() time.Duration {
	if t.IdleConnTimeout <= 0 {
		return defaultIdleConnTimeout
	}
	return t.IdleConnTimeout
}

// lease takes a pooled connection for key, or reports nil when the request must
// dial its own.
func (t *Transport) lease(key string) *persistConn {
	if t.DisableKeepAlives {
		return nil
	}
	return t.pool.get(key, t.idleConnTimeout())
}

// recycle offers a finished connection back to the pool, reporting whether it
// was taken.
func (t *Transport) recycle(pc *persistConn) bool {
	if t.DisableKeepAlives || pc.isBroken() {
		return false
	}
	// Bytes left in the reader mean the server sent something the response
	// framing did not account for. The connection is out of sync; the safe
	// reading is that it is unusable, not that the extra bytes can be skipped.
	if pc.br.Buffered() > 0 {
		return false
	}
	// A deadline set for the last request would otherwise fire during the next
	// one, which presents as an inexplicable timeout on a fresh request.
	if err := pc.conn.SetDeadline(time.Time{}); err != nil {
		return false
	}
	return t.pool.put(pc, t.maxIdleConnsPerHost())
}

// connKey identifies a pool bucket. The destination alone is not enough: the
// same host reached directly and through a proxy are different connections, and
// the plaintext path even writes the request differently for each.
//
// The proxy is resolved from the environment here and again in the dial, so a
// variable that changes mid-process yields a new bucket rather than reusing a
// connection to the previous hop.
func connKey(scheme, host, port string, secure bool) (key string, viaProxy bool, err error) {
	p, err := proxyFor(host, port, secure)
	if err != nil {
		return "", false, err
	}
	via := "direct"
	if p != nil {
		via = net.JoinHostPort(p.Host, p.Port)
		// Only the plaintext path is affected. An https request reaches the
		// origin through a CONNECT tunnel, which is transparent once open.
		viaProxy = !secure
	}
	return scheme + "://" + net.JoinHostPort(host, port) + " via " + via, viaProxy, nil
}

// reusableResponse reports whether the connection can carry another request
// after this response.
//
// The framing test is the important one. A response delimited by connection
// close has no other end marker, so its connection is spent by definition, and
// treating one as reusable would splice the next request onto a body still
// arriving.
func reusableResponse(req *http.Request, resp *http.Response) bool {
	if resp.Close || resp.ProtoMajor < 1 || (resp.ProtoMajor == 1 && resp.ProtoMinor == 0) {
		return false
	}
	if hasToken(resp.Header.Get("Connection"), "close") {
		return false
	}
	if bodyless(req, resp) {
		return true
	}
	if resp.ContentLength >= 0 {
		return true
	}
	for _, te := range resp.TransferEncoding {
		if strings.EqualFold(te, "chunked") {
			return true
		}
	}
	return false
}

// bodyless reports the cases where the status line alone ends the response.
func bodyless(req *http.Request, resp *http.Response) bool {
	if req != nil && strings.EqualFold(req.Method, "HEAD") {
		return true
	}
	switch {
	case resp.StatusCode >= 100 && resp.StatusCode < 200:
		return true
	case resp.StatusCode == http.StatusNoContent, resp.StatusCode == http.StatusNotModified:
		return true
	}
	return false
}

// hasToken reports whether a comma-separated header value carries token.
func hasToken(value, token string) bool {
	for _, field := range strings.Split(value, ",") {
		if strings.EqualFold(strings.TrimSpace(field), token) {
			return true
		}
	}
	return false
}

// poolBody is the response body handed to the caller. Closing it decides the
// connection's fate: back to the pool when the exchange completed cleanly,
// closed otherwise.
type poolBody struct {
	rc   io.ReadCloser
	pc   *persistConn
	t    *Transport
	stop func()

	reusable bool
	sawEOF   bool
	failed   bool
	closed   bool
}

func (b *poolBody) Read(p []byte) (int, error) {
	n, err := b.rc.Read(p)
	switch {
	case err == nil:
	case err == io.EOF:
		b.sawEOF = true
	default:
		b.failed = true
	}
	return n, err
}

func (b *poolBody) Close() error {
	if b.closed {
		return nil
	}
	b.closed = true
	if b.stop != nil {
		b.stop()
	}

	// A caller that closes without reading is normal for HEAD and for a status
	// it only needed the code from. Finishing the body for them keeps the
	// connection usable, as long as finishing it stays cheap.
	if b.reusable && !b.sawEOF && !b.failed {
		b.drain()
	}

	err := b.rc.Close()
	if b.reusable && b.sawEOF && !b.failed && b.t.recycle(b.pc) {
		return err
	}
	if cerr := b.pc.close(); err == nil {
		err = cerr
	}
	return err
}

// drain reads out a body the caller ignored, giving up as soon as it stops
// looking cheap. Giving up simply means the connection is closed instead of
// pooled, so the caps need no precision.
func (b *poolBody) drain() {
	if err := b.pc.conn.SetDeadline(time.Now().Add(drainTimeout)); err != nil {
		b.failed = true
		return
	}
	var buf [512]byte
	read := 0
	for i := 0; i < maxDrainReads && read < maxDrainBytes; i++ {
		n, err := b.rc.Read(buf[:])
		read += n
		if err == io.EOF {
			b.sawEOF = true
			return
		}
		if err != nil {
			b.failed = true
			return
		}
	}
	b.failed = true
}
