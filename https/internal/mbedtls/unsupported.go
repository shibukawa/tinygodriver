//go:build !((tinygo || force_tinygo_logic) && (linux || (darwin && darwinstarttlswith13)))

package mbedtls

import "errors"

// Supported reports whether this build has the mbedTLS backend compiled in.
const Supported = false

var errUnsupported = errors.New("mbedtls: not built for this platform")

// Options mirrors the real type so callers compile everywhere.
type Options struct {
	Host          string
	RootCAsPEM    []byte
	ClientCertPEM []byte
	ClientKeyPEM  []byte
	SkipVerify    bool
	MinVersion    uint16
}

// Session is a placeholder on platforms without the backend.
type Session struct{}

// Handshake always fails in this build.
func Handshake(fd int, opt Options, timeoutNanos int64) (*Session, *Error) {
	return nil, &Error{Class: ClassSetup, Err: errUnsupported}
}

func (s *Session) Read(p []byte, timeoutNanos int64) (int, *Error)  { return 0, nil }
func (s *Session) Write(p []byte, timeoutNanos int64) (int, *Error) { return 0, nil }
func (s *Session) Close()                                           {}

// SelfTest reports that there is nothing to test in this build.
func SelfTest() error { return errUnsupported }

// HWCaps reports that the concept does not apply in this build.
func HWCaps() int { return -1 }
