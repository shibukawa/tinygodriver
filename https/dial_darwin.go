//go:build (tinygo || force_tinygo_logic) && darwin && !darwinstarttlswith13

package https

import (
	"context"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"time"

	"github.com/shibukawa/tinygodriver/netdev"
)

// macOS ships two usable TLS stacks and this build carries both, because
// neither one covers what the package needs on its own:
//
//   - Network.framework negotiates TLS 1.3, but an nw_connection owns DNS, TCP
//     and TLS together, so it cannot start TLS on a socket that has already
//     carried plaintext.
//   - Secure Transport is a byte transformer with caller-supplied I/O, so it
//     can do exactly that, but Apple never gave it TLS 1.3.
//
// dialTLS therefore uses Network.framework and upgradeTLS uses Secure
// Transport. Build with -tags darwinstarttlswith13 to replace both with
// mbedTLS and get TLS 1.3 on the upgrade path too.
const backendName = backendNetwork

const (
	backendNetwork         = "network"
	backendSecureTransport = "securetransport"
)

// defaultOpTimeout bounds a single read or write when no deadline is set, so a
// stalled peer cannot block a goroutine forever.
const defaultOpTimeout = 5 * time.Minute

// dialTLS opens a verified TLS connection. Network.framework owns DNS, TCP and
// TLS on this path.
func dialTLS(ctx context.Context, host, port string, cfg *Config, timeout time.Duration) (net.Conn, error) {
	return dialNetworkFramework(ctx, host, port, cfg, timeout)
}

// upgradeTLS wraps an already connected socket in TLS, which is what in-band
// upgrade protocols such as PostgreSQL and MySQL STARTTLS need. The descriptor
// may already have carried plaintext.
//
// The descriptor must come from netdev: TinyGo's net package does not implement
// SyscallConn, so there is no way to recover one from a net.Conn.
//
// On success the returned net.Conn owns fd and closes it. On failure fd is
// left untouched, so the caller can still fall back to plaintext.
//
// This path is Secure Transport, so it tops out at TLS 1.2 unless the build
// carries -tags darwinstarttlswith13.
func upgradeTLS(fd int, host string, cfg *Config, timeout time.Duration) (net.Conn, error) {
	return upgradeSecureTransport(fd, host, cfg, timeout)
}

// Both backends take the descriptor from netdev rather than net.Dial.
var (
	dialerOnce sync.Once
	dialer     *netdev.Device
)

func socketDialer() *netdev.Device {
	dialerOnce.Do(func() { dialer = netdev.New() })
	return dialer
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

func (c *Config) certificates() []KeyPair {
	if c == nil {
		return nil
	}
	return c.Certificates
}

func timeoutNanos(deadline time.Time) (int64, bool) {
	if deadline.IsZero() {
		return int64(defaultOpTimeout), true
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return 0, false
	}
	return remaining.Nanoseconds(), true
}

// effectiveTimeout folds a context deadline into the caller's timeout.
func effectiveTimeout(ctx context.Context, timeout time.Duration) time.Duration {
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining < timeout {
			return remaining
		}
	}
	return timeout
}

type placeholderAddr string

func (placeholderAddr) Network() string  { return "tcp" }
func (a placeholderAddr) String() string { return string(a) }

// Secure Transport OSStatus values. Network.framework reports the same numbers
// through nw_error_get_error_code, so one mapping serves both backends.
const (
	errSSLProtocol          = -9800
	errSSLNegotiation       = -9801
	errSSLFatalAlert        = -9802
	errSSLXCertChainInvalid = -9807
	errSSLBadCert           = -9808
	errSSLUnknownRootCert   = -9812
	errSSLNoRootCert        = -9813
	errSSLCertExpired       = -9814
	errSSLCertNotYetValid   = -9815
	errSSLPeerUnknownCA     = -9825
	errSSLPeerAccessDenied  = -9826
	errSSLHostNameMismatch  = -9843
	errSSLBadConfiguration  = -9848
	errSSLNetworkTimeout    = -9853
)

func classifyOSStatus(status int) error {
	switch status {
	case errSSLHostNameMismatch:
		return ErrHostnameMismatch
	case errSSLCertExpired, errSSLCertNotYetValid:
		return ErrCertificateExpired
	case errSSLXCertChainInvalid, errSSLUnknownRootCert, errSSLNoRootCert, errSSLPeerUnknownCA:
		return ErrUntrustedRoot
	case errSSLBadCert:
		return ErrCertificateInvalid
	case errSSLPeerAccessDenied:
		return ErrClientCertificateRejected
	case errSSLProtocol, errSSLBadConfiguration:
		return ErrProtocolVersion
	case errSSLNetworkTimeout:
		return errTimeout
	case errSSLNegotiation, errSSLFatalAlert:
		return ErrHandshakeFailed
	default:
		return ErrHandshakeFailed
	}
}
