//go:build (tinygo || force_tinygo_logic) && windows

package https

import (
	"context"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"time"

	"github.com/shibukawa/tinygodriver/internal/schannel"
	"github.com/shibukawa/tinygodriver/netdev"
)

const backendName = "schannel"

// Windows is the one platform where dialing and upgrading are the same code
// path. Schannel is reached through SSPI, which is a buffer transformer: it
// produces and consumes token bytes and never learns that a socket exists.
// A socket that has already carried plaintext is therefore no different to it
// from a fresh one, so STARTTLS needs no second backend the way macOS does.
//
// The protocol version is whatever the OS negotiates. Schannel reaches TLS 1.3
// on Windows 11 and Server 2022 when the credential is acquired through
// SCH_CREDENTIALS; older builds, and implementations that reject that
// structure, fall back to SCHANNEL_CRED and cap at TLS 1.2.

// The socket comes from netdev rather than net.Dial because TinyGo's net
// package does not implement SyscallConn, so there is no way to recover the
// descriptor from a net.Conn.
var (
	dialerOnce sync.Once
	dialer     *netdev.Device
)

func socketDialer() *netdev.Device {
	dialerOnce.Do(func() { dialer = netdev.New() })
	return dialer
}

func dialTLS(ctx context.Context, host, port string, cfg *Config, timeout time.Duration) (net.Conn, error) {
	timeout = effectiveTimeout(ctx, timeout)
	if timeout <= 0 {
		return nil, &Error{Op: "dial", Host: host, Backend: backendName, Err: errTimeout}
	}

	opt, err := schannelOptions(host, cfg)
	if err != nil {
		return nil, &Error{Op: "dial", Host: host, Backend: backendName, Err: err}
	}

	fd, dev, err := dialSocket(host, port)
	if err != nil {
		return nil, &Error{Op: "dial", Host: host, Backend: backendName, Err: err}
	}

	sess, sErr := schannel.Handshake(fd, opt, timeout.Nanoseconds())
	if sErr != nil {
		dev.Close(fd)
		return nil, schannelError("dial", host, sErr)
	}
	return &scConn{sess: sess, fd: fd, dev: dev, host: host, port: port}, nil
}

// upgradeTLS wraps an already connected socket in TLS, which is what in-band
// upgrade protocols such as PostgreSQL and MySQL STARTTLS need. The descriptor
// may already have carried plaintext.
//
// On success the returned net.Conn owns fd. On failure fd is untouched so the
// caller can still fall back to plaintext.
func upgradeTLS(fd int, host string, cfg *Config, timeout time.Duration) (net.Conn, error) {
	if timeout <= 0 {
		return nil, &Error{Op: "upgrade", Host: host, Backend: backendName, Err: errTimeout}
	}

	opt, err := schannelOptions(host, cfg)
	if err != nil {
		return nil, &Error{Op: "upgrade", Host: host, Backend: backendName, Err: err}
	}

	sess, sErr := schannel.Handshake(fd, opt, timeout.Nanoseconds())
	if sErr != nil {
		return nil, schannelError("upgrade", host, sErr)
	}
	return &scConn{sess: sess, fd: fd, dev: socketDialer(), host: host}, nil
}

// schannelOptions translates a Config into what the backend takes. PEM is
// decoded here so the C layer never needs a base64 decoder.
func schannelOptions(host string, cfg *Config) (schannel.Options, error) {
	if !schannel.Supported {
		// Asking for the native backend in a cgo-free build. There is no
		// Schannel binding to reach, and this must not quietly become a
		// plaintext or unverified connection.
		return schannel.Options{}, ErrPlatformNotSupported
	}
	opt := schannel.Options{
		Host:       host,
		SkipVerify: cfg.skipVerify(),
		MinVersion: uint16(cfg.minVersion()),
	}
	if cfg != nil && cfg.ServerName != "" {
		opt.Host = cfg.ServerName
	}

	ders, err := cfg.rootCADER()
	if err != nil {
		return opt, err
	}
	opt.RootCAsDER = ders
	if cfg != nil {
		opt.RootCAsOnly = cfg.RootCAsOnly
	}

	if certs := cfg.certificates(); len(certs) > 0 {
		certDER, keyDER, pkcs8, err := clientKeyPair(certs[0])
		if err != nil {
			return opt, err
		}
		opt.ClientCertDER = certDER
		opt.ClientKeyDER = keyDER
		opt.ClientKeyPKCS8 = pkcs8
	}
	return opt, nil
}

// clientKeyPair decodes one PEM certificate and its private key.
//
// Schannel wants a CERT_CONTEXT carrying a CNG key handle, and the only key
// blob CryptDecodeObjectEx hands straight to NCryptImportKey is RSA. An EC key
// would mean assembling a BCRYPT_ECCPRIVATE_BLOB by hand, so it is refused
// rather than silently mishandled.
func clientKeyPair(kp KeyPair) ([]byte, []byte, bool, error) {
	var certDER []byte
	rest := kp.CertPEM
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type == "CERTIFICATE" {
			certDER = block.Bytes
			break
		}
	}
	if certDER == nil {
		return nil, nil, false, errors.New("https: no CERTIFICATE block in client certificate")
	}

	block, _ := pem.Decode(kp.KeyPEM)
	if block == nil {
		return nil, nil, false, errors.New("https: no PEM block in client key")
	}
	switch block.Type {
	case "RSA PRIVATE KEY":
		return certDER, block.Bytes, false, nil
	case "PRIVATE KEY":
		// PKCS#8. The C layer unwraps it and rejects anything but RSA.
		return certDER, block.Bytes, true, nil
	default:
		return nil, nil, false, ErrClientCertificateUnsupported
	}
}

// dialSocket connects a TCP socket and returns its descriptor. An invalid
// address makes netdev resolve host itself.
func dialSocket(host, port string) (int, *netdev.Device, error) {
	portNum, err := strconv.Atoi(port)
	if err != nil {
		return -1, nil, err
	}
	dev := socketDialer()
	fd, err := dev.Socket(netdev.AF_INET, netdev.SOCK_STREAM, netdev.IPPROTO_TCP)
	if err != nil {
		return -1, nil, err
	}
	addr := netip.AddrPortFrom(netip.Addr{}, uint16(portNum))
	if ip, err := netip.ParseAddr(host); err == nil {
		addr = netip.AddrPortFrom(ip, uint16(portNum))
	}
	if err := dev.Connect(fd, host, addr); err != nil {
		dev.Close(fd)
		return -1, nil, err
	}
	return fd, dev, nil
}

func (c *Config) skipVerify() bool {
	return c != nil && c.InsecureSkipVerify
}

func (c *Config) certificates() []KeyPair {
	if c == nil {
		return nil
	}
	return c.Certificates
}

// scConn adapts a Schannel session to net.Conn. It owns the descriptor it was
// handed.
type scConn struct {
	// ioMu serializes access to the session; state carries the deadlines behind
	// its own lock so SetDeadline never waits for a blocked read.
	ioMu  sync.Mutex
	state connState

	sess *schannel.Session
	fd   int
	dev  *netdev.Device
	host string
	port string
}

var _ net.Conn = (*scConn)(nil)

func (c *scConn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	ns, err := c.state.readBudget()
	if err != nil {
		return 0, stateError("read", c.host, backendName, err)
	}
	c.ioMu.Lock()
	defer c.ioMu.Unlock()

	n, sErr := c.sess.Read(p, ns)
	if sErr != nil {
		return 0, schannelIOError("read", c.host, sErr)
	}
	if n == 0 {
		return 0, io.EOF
	}
	return n, nil
}

func (c *scConn) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	ns, err := c.state.writeBudget()
	if err != nil {
		return 0, stateError("write", c.host, backendName, err)
	}
	c.ioMu.Lock()
	defer c.ioMu.Unlock()

	n, sErr := c.sess.Write(p, ns)
	if sErr != nil {
		return n, schannelIOError("write", c.host, sErr)
	}
	return n, nil
}

func (c *scConn) Close() error {
	if !c.state.close() {
		return nil
	}
	c.ioMu.Lock()
	defer c.ioMu.Unlock()
	c.sess.Close()
	return c.dev.Close(c.fd)
}

func (c *scConn) SetDeadline(t time.Time) error      { return c.state.setDeadline(t) }
func (c *scConn) SetReadDeadline(t time.Time) error  { return c.state.setReadDeadline(t) }
func (c *scConn) SetWriteDeadline(t time.Time) error { return c.state.setWriteDeadline(t) }

func (c *scConn) LocalAddr() net.Addr { return placeholderAddr("") }

func (c *scConn) RemoteAddr() net.Addr {
	if c.port == "" {
		return placeholderAddr(c.host)
	}
	return placeholderAddr(net.JoinHostPort(c.host, c.port))
}

type placeholderAddr string

func (placeholderAddr) Network() string  { return "tcp" }
func (a placeholderAddr) String() string { return string(a) }

func schannelError(op, host string, e *schannel.Error) error {
	err := &Error{Op: op, Host: host, Backend: backendName, Code: e.Status}
	switch e.Class {
	case schannel.ClassTimeout:
		err.Err = errTimeout
	case schannel.ClassCA:
		err.Err = ErrCertificateInvalid
	case schannel.ClassClientCert:
		if uint32(e.Status) == schannel.StatusKeyUnsupported {
			err.Err = ErrClientCertificateUnsupported
		} else {
			err.Err = ErrClientCertificateRejected
		}
	case schannel.ClassHandshake:
		err.Err = classifyStatus(e.Status)
	case schannel.ClassSetup:
		err.Err = classifySetupStatus(e.Status)
	case schannel.ClassClosed:
		err.Err = net.ErrClosed
	default:
		err.Err = ErrHandshakeFailed
	}
	return err
}

func schannelIOError(op, host string, e *schannel.Error) error {
	err := &Error{Op: op, Host: host, Backend: backendName, Code: e.Status}
	switch e.Class {
	case schannel.ClassTimeout:
		err.Err = errTimeout
	case schannel.ClassClosed:
		err.Err = net.ErrClosed
	default:
		if uint32(e.Status) == secEContextExpired {
			err.Err = net.ErrClosed
		} else {
			err.Err = errors.New("https: encrypted I/O failed")
		}
	}
	return err
}

// SSPI status codes. Unsigned because an HRESULT does not fit in a positive
// int32 constant; the bridge reports it through a C int, so the comparison
// below converts back. Keeping them unsigned also keeps them readable as the
// hex values the Windows headers use.
const (
	secEUnsupportedFunction uint32 = 0x80090302
	secEInvalidToken        uint32 = 0x80090308
	secELogonDenied         uint32 = 0x8009030C
	secENoCredentials       uint32 = 0x8009030E
	secEContextExpired      uint32 = 0x80090317
	secEWrongPrincipal      uint32 = 0x80090322
	secEUntrustedRoot       uint32 = 0x80090325
	secEIllegalMessage      uint32 = 0x80090326
	secECertUnknown         uint32 = 0x80090327
	secECertExpired         uint32 = 0x80090328
	secEAlgorithmMismatch   uint32 = 0x80090331
)

// crypt32 chain policy codes.
const (
	certEExpired            uint32 = 0x800B0101
	certENotTimeValidStatus uint32 = 0x800B0102
	certEPathLenConst       uint32 = 0x800B0104
	certEMalformed          uint32 = 0x800B0108
	certEUntrustedRoot      uint32 = 0x800B0109
	certEChaining           uint32 = 0x800B010A
	trustEFail              uint32 = 0x800B010B
	certERevoked            uint32 = 0x800B010C
	certEUntrustedTestRoot  uint32 = 0x800B010D
	certERevocationFailure  uint32 = 0x800B010E
	certECNNoMatch          uint32 = 0x800B010F
	certEWrongUsage         uint32 = 0x800B0110
	trustEExplicitDistrust  uint32 = 0x800B0111
	certEUntrustedCA        uint32 = 0x800B0112
	certEInvalidName        uint32 = 0x800B0114
	trustECertSignature     uint32 = 0x80096004
	trustEBasicConstraints  uint32 = 0x80096019
)

// CERT_TRUST_* bits. The bridge reports these only when the chain engine
// flagged something the SSL policy check let through, so they are a fallback
// rather than the usual path. They are small positive values, which is what
// distinguishes them from the HRESULTs above.
const (
	certTrustIsNotTimeValid      = 0x00000001
	certTrustIsRevoked           = 0x00000004
	certTrustIsNotSignatureValid = 0x00000008
	certTrustIsNotValidForUsage  = 0x00000010
	certTrustIsUntrustedRoot     = 0x00000020
	certTrustIsExplicitDistrust  = 0x04000000
	certTrustIsPartialChain      = 0x00010000
)

func classifyStatus(status int) error {
	// A positive value is a CERT_TRUST_* bitmask, not an HRESULT: every code
	// below has the high bit set and so arrives negative.
	if status > 0 {
		return classifyTrustBits(status)
	}
	switch uint32(status) {
	case certECNNoMatch, secEWrongPrincipal, certEInvalidName:
		return ErrHostnameMismatch
	case certEExpired, secECertExpired, certENotTimeValidStatus:
		return ErrCertificateExpired
	case certEUntrustedRoot, secEUntrustedRoot, certEUntrustedTestRoot,
		certEUntrustedCA, certEChaining, trustEExplicitDistrust:
		return ErrUntrustedRoot
	case certERevoked, certERevocationFailure, certEMalformed, certEWrongUsage,
		certEPathLenConst, trustECertSignature, trustEBasicConstraints,
		secECertUnknown, trustEFail:
		return ErrCertificateInvalid
	case secELogonDenied, secENoCredentials:
		return ErrClientCertificateRejected
	case secEAlgorithmMismatch, secEUnsupportedFunction:
		return ErrProtocolVersion
	case secEIllegalMessage, secEInvalidToken:
		return ErrHandshakeFailed
	default:
		return ErrHandshakeFailed
	}
}

func classifyTrustBits(bits int) error {
	switch {
	case bits&certTrustIsNotTimeValid != 0:
		return ErrCertificateExpired
	case bits&(certTrustIsUntrustedRoot|certTrustIsPartialChain|certTrustIsExplicitDistrust) != 0:
		return ErrUntrustedRoot
	case bits&(certTrustIsRevoked|certTrustIsNotSignatureValid|certTrustIsNotValidForUsage) != 0:
		return ErrCertificateInvalid
	default:
		return ErrCertificateInvalid
	}
}

// classifySetupStatus covers failures before any bytes move: acquiring the
// credential and querying the stream sizes.
func classifySetupStatus(status int) error {
	switch uint32(status) {
	case secEAlgorithmMismatch, secEUnsupportedFunction:
		// The usual cause is a MinVersion the installed Schannel cannot offer.
		return ErrProtocolVersion
	default:
		return ErrHandshakeFailed
	}
}
