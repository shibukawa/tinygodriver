//go:build windows

package netdev

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/shibukawa/tinygodriver/internal/schannel"
)

// IPPROTO_TLS on windows uses Schannel, which ships with the OS. There is no
// package manager dependency and no vendored TLS stack, the same property the
// darwin backend has.
//
// Schannel is a buffer transformer reached through SSPI: it never sees the
// socket, it only produces and consumes token bytes. That fits this seam,
// which hands over an already connected descriptor, and it means TLS 1.3 is
// available where the OS supports it, unlike Secure Transport on darwin.

// handshakeTimeout bounds the handshake, and each read and write, so a stalled
// peer cannot block a goroutine forever. netdev's own deadlines are applied by
// the caller before Send and Recv.
const handshakeTimeout = 30 * time.Second

const tlsOpTimeout = 5 * time.Minute

func sysTLSConnect(fd int, hostname string) (uintptr, error) {
	if !schannel.Supported {
		// A cgo-free build has no Schannel binding. Say so plainly rather than
		// letting the shim report a setup failure with a zero status code.
		return 0, ErrProtocolNotSupported
	}
	if hostname == "" {
		return 0, errors.New("tls: empty server name")
	}

	opt := schannel.Options{Host: hostname}
	// Schannel verifies against the Windows certificate store, which knows
	// nothing of SSL_CERT_FILE. netdev has always honored it, so keep doing so
	// by loading the file as an extra anchor.
	if ders, err := extraAnchors(); err != nil {
		return 0, err
	} else if len(ders) > 0 {
		opt.RootCAsDER = ders
	}

	sess, err := schannel.Handshake(fd, opt, handshakeTimeout.Nanoseconds())
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

// extraAnchors reads SSL_CERT_FILE, which the other backends honor too.
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

// tlsErrorFromStatus names the failure and carries the SECURITY_STATUS, which
// is what makes a Schannel failure diagnosable at all: the status is the only
// thing that distinguishes an untrusted root from a name mismatch.
func tlsErrorFromStatus(e *schannel.Error) error {
	switch e.Class {
	case schannel.ClassTimeout:
		return ErrTimeout
	case schannel.ClassAlloc:
		return errors.New("tls: failed to allocate client state")
	case schannel.ClassSetup:
		return fmt.Errorf("tls: session setup failed (status 0x%08x)", uint32(e.Status))
	case schannel.ClassCA:
		return errors.New("tls: trust anchors rejected")
	case schannel.ClassClientCert:
		return errors.New("tls: client certificate rejected")
	case schannel.ClassHandshake:
		return fmt.Errorf("tls: handshake or certificate verification failed (status 0x%08x)", uint32(e.Status))
	case schannel.ClassClosed:
		return errors.New("tls: session closed")
	default:
		return fmt.Errorf("tls: encrypted I/O failed (status 0x%08x)", uint32(e.Status))
	}
}
