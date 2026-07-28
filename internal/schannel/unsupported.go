//go:build !windows

package schannel

import "errors"

// Supported reports whether this build has the backend compiled in.
const Supported = false

const (
	ProtocolTLS12 = 0x0303
	ProtocolTLS13 = 0x0304
)

const StatusKeyUnsupported uint32 = 0x80090302

var errUnsupported = errors.New("schannel: not built for this platform")

// Options mirrors the real type so callers compile everywhere.
type Options struct {
	Host           string
	RootCAsDER     [][]byte
	RootCAsOnly    bool
	SkipVerify     bool
	MinVersion     uint16
	ClientCertDER  []byte
	ClientKeyDER   []byte
	ClientKeyPKCS8 bool
}

// Session is a placeholder on platforms without the backend.
type Session struct{}

// Class is the coarse failure category from the C layer.
type Class int

const (
	ClassOK Class = iota
	ClassAlloc
	ClassSetup
	ClassCA
	ClassClientCert
	ClassHandshake
	ClassTimeout
	ClassIO
	ClassClosed
)

// Error carries the class and the raw SECURITY_STATUS or HRESULT.
type Error struct {
	Class  Class
	Status int
}

func (e *Error) Error() string { return errUnsupported.Error() }
func (e *Error) Timeout() bool { return false }

// Handshake always fails in this build.
func Handshake(fd int, opt Options, timeoutNanos int64) (*Session, *Error) {
	return nil, &Error{Class: ClassSetup}
}

func (s *Session) Read(p []byte, timeoutNanos int64) (int, *Error)  { return 0, nil }
func (s *Session) Write(p []byte, timeoutNanos int64) (int, *Error) { return 0, nil }
func (s *Session) Close()                                           {}
