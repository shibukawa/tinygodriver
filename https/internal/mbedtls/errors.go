package mbedtls

import "fmt"

// Class is the coarse failure category reported by the C layer. The caller
// maps it, together with VerifyFlags, onto the package-level sentinels.
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

func (c Class) String() string {
	switch c {
	case ClassOK:
		return "ok"
	case ClassAlloc:
		return "allocation failed"
	case ClassSetup:
		return "setup failed"
	case ClassCA:
		return "trust anchors rejected"
	case ClassClientCert:
		return "client certificate rejected"
	case ClassHandshake:
		return "handshake failed"
	case ClassTimeout:
		return "timeout"
	case ClassIO:
		return "encrypted I/O failed"
	case ClassClosed:
		return "session closed"
	}
	return "unknown"
}

// X.509 verification flags, the subset worth distinguishing. mbedTLS reports
// every verification problem as one coarse status code, so this bitmask is the
// only way to tell an expired certificate from a name mismatch.
const (
	BadCertExpired    = 0x01
	BadCertRevoked    = 0x02
	BadCertCNMismatch = 0x04
	BadCertNotTrusted = 0x08
	BadCertFuture     = 0x200
)

// Error carries the class, the raw mbedTLS status, and the verification
// bitmask.
type Error struct {
	Class       Class
	Code        int // negative mbedTLS status, zero when not applicable
	VerifyFlags uint32
	Err         error // set only when there is no mbedTLS status to report
}

func (e *Error) Error() string {
	if e.Err != nil {
		return "mbedtls: " + e.Err.Error()
	}
	if e.Code != 0 {
		return fmt.Sprintf("mbedtls: %s (status -0x%04x, verify 0x%x)",
			e.Class, -e.Code, e.VerifyFlags)
	}
	return "mbedtls: " + e.Class.String()
}

// Timeout reports whether this was a deadline expiry.
func (e *Error) Timeout() bool { return e.Class == ClassTimeout }
