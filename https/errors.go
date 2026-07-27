package https

import (
	"errors"
	"fmt"
)

// Sentinel errors. Native backend status codes are mapped onto these so
// application code can branch with errors.Is regardless of platform.
var (
	ErrHandshakeFailed           = errors.New("https: TLS handshake failed")
	ErrCertificateInvalid        = errors.New("https: certificate invalid")
	ErrCertificateExpired        = errors.New("https: certificate expired")
	ErrHostnameMismatch          = errors.New("https: certificate hostname mismatch")
	ErrUntrustedRoot             = errors.New("https: certificate signed by untrusted root")
	ErrClientCertificateRejected = errors.New("https: client certificate rejected")
	ErrProtocolVersion           = errors.New("https: TLS protocol version not supported")
	ErrPlatformNotSupported      = errors.New("https: platform not supported")

	// ErrClientCertificateUnsupported reports that the active backend cannot
	// offer a client certificate. Network.framework needs a SecIdentityRef,
	// which requires importing the key into a keychain.
	ErrClientCertificateUnsupported = errors.New("https: client certificates not supported by this backend")
)

// Error carries the native status code alongside a mapped sentinel.
type Error struct {
	Op      string // "dial", "handshake", "read", "write"
	Host    string
	Backend string
	Code    int // native status code, for diagnosis
	Err     error
}

func (e *Error) Error() string {
	msg := fmt.Sprintf("https: %s %s: %v", e.Op, e.Host, e.Err)
	if e.Code != 0 {
		msg = fmt.Sprintf("%s (%s status %d)", msg, e.Backend, e.Code)
	}
	return msg
}

func (e *Error) Unwrap() error { return e.Err }

// Timeout reports whether the error was a timeout, satisfying net.Error for
// callers that check it.
func (e *Error) Timeout() bool { return errors.Is(e.Err, errTimeout) }

// Temporary is retained for net.Error compatibility.
func (e *Error) Temporary() bool { return false }

var errTimeout = &timeoutError{}

type timeoutError struct{}

func (*timeoutError) Error() string   { return "https: i/o timeout" }
func (*timeoutError) Timeout() bool   { return true }
func (*timeoutError) Temporary() bool { return true }
