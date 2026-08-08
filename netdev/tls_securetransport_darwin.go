//go:build darwin

package netdev

import (
	"errors"
	"os"
	"strconv"
	"time"

	"github.com/shibukawa/tinygodriver/internal/securetransport"
)

// IPPROTO_TLS on darwin uses Secure Transport rather than OpenSSL, so a
// binary needs no Homebrew openssl@3 at build or run time.
//
// Secure Transport is the only OS-provided option that fits: this seam hands
// over an already connected descriptor, and an nw_connection owns DNS, TCP and
// TLS as one unit and cannot adopt one. The cost is that Secure Transport
// stops at TLS 1.2.
//
// The trade against the previous OpenSSL implementation:
//   - gained: no package manager dependency
//   - lost: TLS 1.3, which OpenSSL negotiated

// handshakeTimeout bounds the handshake, and each read and write, so a stalled
// peer cannot block a goroutine forever. netdev's own deadlines are applied by
// the caller before Send and Recv.
const handshakeTimeout = 30 * time.Second

const tlsOpTimeout = 5 * time.Minute

func sysTLSConnect(fd int, hostname string) (uintptr, error) {
	if hostname == "" {
		return 0, errors.New("tls: empty server name")
	}

	opt := securetransport.Options{Host: hostname}
	// Secure Transport verifies against the keychain, which knows nothing of
	// SSL_CERT_FILE. netdev has always honored it, so keep doing so by loading
	// the file as an extra anchor.
	if ders, err := extraAnchors(); err != nil {
		return 0, err
	} else if len(ders) > 0 {
		opt.RootCAsDER = ders
	}

	sess, err := securetransport.Handshake(fd, opt, handshakeTimeout.Nanoseconds())
	if err != nil {
		return 0, tlsErrorFromStatus(err)
	}
	return sessionHandle(sess), nil
}

func sysTLSSend(state uintptr, buf []byte) (int, error) {
	sess := sessionFromHandle(state)
	if sess == nil || len(buf) == 0 {
		return 0, nil
	}
	n, err := sess.Write(buf, tlsOpTimeout.Nanoseconds())
	if err != nil {
		return -1, tlsErrorFromStatus(err)
	}
	return n, nil
}

func sysTLSRecv(state uintptr, buf []byte) (int, error) {
	sess := sessionFromHandle(state)
	if sess == nil || len(buf) == 0 {
		return 0, nil
	}
	n, err := sess.Read(buf, tlsOpTimeout.Nanoseconds())
	if err != nil {
		return -1, tlsErrorFromStatus(err)
	}
	return n, nil
}

func sysTLSClose(state uintptr) {
	if sess := sessionFromHandle(state); sess != nil {
		sess.Close()
		releaseHandle(state)
	}
}

// extraAnchors reads SSL_CERT_FILE, which netdev's OpenSSL implementation
// honored through SSL_CTX_set_default_verify_paths.
func extraAnchors() ([][]byte, error) {
	path := os.Getenv("SSL_CERT_FILE")
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return pemToDER(data)
}

// tlsErrorFromStatus names the failure and carries the OSStatus. The previous
// OpenSSL implementation collapsed everything into four generic strings, which
// made diagnosis guesswork.
func tlsErrorFromStatus(e *securetransport.Error) error {
	switch e.Class {
	case securetransport.ClassTimeout:
		return ErrTimeout
	case securetransport.ClassAlloc:
		return errors.New("tls: failed to allocate client state")
	case securetransport.ClassSetup:
		return errors.New("tls: session setup failed (OSStatus " + strconv.Itoa(int(e.Status)) + ")")
	case securetransport.ClassCA:
		return errors.New("tls: trust anchors rejected")
	case securetransport.ClassHandshake:
		return errors.New("tls: handshake or certificate verification failed (OSStatus " + strconv.Itoa(int(e.Status)) + ")")
	case securetransport.ClassClosed:
		return errors.New("tls: session closed")
	default:
		return errors.New("tls: encrypted I/O failed (OSStatus " + strconv.Itoa(int(e.Status)) + ")")
	}
}
