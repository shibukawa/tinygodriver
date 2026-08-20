//go:build linux && !tinygo

package netdev

import (
	"crypto/tls"
	"errors"
	"io"
	"net"
	"sync"
	"time"
)

// IPPROTO_TLS on host-Go linux uses crypto/tls.
//
// The OpenSSL adapter this replaces carried the tag `linux && !tinygo`, so it
// was only ever reachable from a host-Go build: decision:linux-mbedtls found
// that a TinyGo binary cannot call the distribution's shared OpenSSL at all,
// and the TinyGo side of this seam is a stub that refuses IPPROTO_TLS. It
// therefore made libssl a build and runtime dependency of every linux
// development and CI environment in order to serve a path that never shipped.
//
// crypto/tls is already linked into this build, needs no package, and honors
// SSL_CERT_FILE and SSL_CERT_DIR through x509.SystemCertPool exactly as
// SSL_CTX_set_default_verify_paths did, so the trust behavior is unchanged.
//
// darwin and windows deliberately keep their OS stacks. There the same file
// builds under TinyGo too, so the host-Go test exercises shipping code. Linux
// is the only platform where it does not, which is what makes the standard
// library the right answer here and the wrong one there.

// handshakeTimeout bounds the handshake, and tlsOpTimeout each read and write,
// so a stalled peer cannot block a goroutine forever. netdev's own deadlines
// are applied by the caller before Send and Recv.
const (
	handshakeTimeout = 30 * time.Second
	tlsOpTimeout     = 5 * time.Minute
	closeTimeout     = 5 * time.Second
)

// fdConn drives an already connected netdev descriptor as a net.Conn so
// crypto/tls can transform bytes over it.
//
// It deliberately does not own the descriptor. Device.Close calls sysTLSClose
// and then closes the descriptor itself, so closing it here would be a double
// close. net.FileConn is unusable for the same reason and for a second one: it
// dups the descriptor onto the netpoller, while Send and Recv still select on
// the original, and the dup shares the file description so putting it in
// non-blocking mode would change the original too.
type fdConn struct {
	fd int

	mu     sync.Mutex
	ttl    time.Duration // per-op bound used when no explicit deadline is set
	rd, wd time.Time
}

func (c *fdConn) setTTL(d time.Duration) {
	c.mu.Lock()
	c.ttl = d
	c.mu.Unlock()
}

func (c *fdConn) deadline(read bool) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	explicit := c.wd
	if read {
		explicit = c.rd
	}
	if !explicit.IsZero() {
		return explicit
	}
	if c.ttl <= 0 {
		return time.Time{}
	}
	return time.Now().Add(c.ttl)
}

func (c *fdConn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if err := waitRead(c.fd, c.deadline(true)); err != nil {
		return 0, err
	}
	n, err := sysRecv(c.fd, p, 0)
	switch {
	case n > 0:
		return n, nil
	case err != nil:
		return 0, err
	default:
		return 0, io.EOF
	}
}

func (c *fdConn) Write(p []byte) (int, error) {
	written := 0
	for written < len(p) {
		if err := waitWrite(c.fd, c.deadline(false)); err != nil {
			return written, err
		}
		n, err := sysSend(c.fd, p[written:], 0)
		if n > 0 {
			written += n
			continue
		}
		if err != nil {
			return written, err
		}
		// No progress and no error would spin forever.
		return written, io.ErrUnexpectedEOF
	}
	return written, nil
}

// Close is a no-op: the descriptor belongs to the Device.
func (c *fdConn) Close() error { return nil }

func (c *fdConn) SetDeadline(t time.Time) error {
	c.mu.Lock()
	c.rd, c.wd = t, t
	c.mu.Unlock()
	return nil
}

func (c *fdConn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	c.rd = t
	c.mu.Unlock()
	return nil
}

func (c *fdConn) SetWriteDeadline(t time.Time) error {
	c.mu.Lock()
	c.wd = t
	c.mu.Unlock()
	return nil
}

// The addresses netdev tracks live on the socket, not here, and crypto/tls
// never consults them.
func (c *fdConn) LocalAddr() net.Addr  { return fdAddr{} }
func (c *fdConn) RemoteAddr() net.Addr { return fdAddr{} }

type fdAddr struct{}

func (fdAddr) Network() string { return "tcp" }
func (fdAddr) String() string  { return "" }

// tlsSession is what the uintptr handle refers to. The seam passes state as a
// uintptr, which cannot hold a Go pointer, so sessions live in a table.
type tlsSession struct {
	conn *tls.Conn
	raw  *fdConn
}

var (
	sessionMu   sync.RWMutex
	sessions            = map[uintptr]*tlsSession{}
	sessionNext uintptr = 1
)

func sessionHandle(s *tlsSession) uintptr {
	sessionMu.Lock()
	defer sessionMu.Unlock()
	h := sessionNext
	sessionNext++
	sessions[h] = s
	return h
}

// The lookup runs on every Send and Recv, so it takes the read side only.
func sessionFromHandle(h uintptr) *tlsSession {
	sessionMu.RLock()
	defer sessionMu.RUnlock()
	return sessions[h]
}

func releaseHandle(h uintptr) {
	sessionMu.Lock()
	defer sessionMu.Unlock()
	delete(sessions, h)
}

func sysTLSConnect(fd int, hostname string) (uintptr, error) {
	if hostname == "" {
		return 0, errors.New("tls: empty server name")
	}

	raw := &fdConn{fd: fd, ttl: handshakeTimeout}
	// A nil RootCAs means the system pool, which is what
	// SSL_CTX_set_default_verify_paths gave the previous implementation.
	conn := tls.Client(raw, &tls.Config{
		ServerName: hostname,
		MinVersion: tls.VersionTLS12,
	})
	if err := conn.Handshake(); err != nil {
		return 0, tlsHandshakeError(err)
	}
	raw.setTTL(tlsOpTimeout)
	return sessionHandle(&tlsSession{conn: conn, raw: raw}), nil
}

func sysTLSSend(state uintptr, buf []byte) (int, error) {
	s := sessionFromHandle(state)
	if s == nil || len(buf) == 0 {
		return 0, nil
	}
	n, err := s.conn.Write(buf)
	if err != nil {
		return -1, tlsIOError(err)
	}
	return n, nil
}

func sysTLSRecv(state uintptr, buf []byte) (int, error) {
	s := sessionFromHandle(state)
	if s == nil || len(buf) == 0 {
		return 0, nil
	}
	n, err := s.conn.Read(buf)
	if n > 0 {
		return n, nil
	}
	// Device.Recv turns a zero-length read with no error into io.EOF, so a
	// clean close is reported that way rather than as a failure.
	if err == nil || errors.Is(err, io.EOF) {
		return 0, nil
	}
	return -1, tlsIOError(err)
}

func sysTLSClose(state uintptr) {
	s := sessionFromHandle(state)
	if s == nil {
		return
	}
	// close_notify is worth sending but not worth waiting five minutes for on a
	// peer that has already gone.
	s.raw.setTTL(closeTimeout)
	s.conn.Close()
	releaseHandle(state)
}

// Sentinel prefixes for the wrapped forms below.
var (
	errTLSHandshakeFailed = errors.New("tls: handshake or certificate verification failed")
	errTLSIOFailed        = errors.New("tls: encrypted I/O failed")
)

// tlsHandshakeError names the failure and keeps the cause. The previous
// OpenSSL implementation collapsed everything into four generic strings, which
// made diagnosis guesswork.
func tlsHandshakeError(err error) error {
	if errors.Is(err, ErrTimeout) {
		return ErrTimeout
	}
	return &wrappedError{sentinel: errTLSHandshakeFailed, cause: err}
}

func tlsIOError(err error) error {
	if errors.Is(err, ErrTimeout) {
		return ErrTimeout
	}
	return &wrappedError{sentinel: errTLSIOFailed, cause: err}
}
