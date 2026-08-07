//go:build (tinygo || force_tinygo_logic) && (darwin || (linux && !wasip2) || windows)

package https

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

// CONNECT tunnelling for the native path.
//
// This needs no backend support at all, because DialPlain and Upgrade are
// already the two primitives it wants: a plaintext socket that carries its
// descriptor, and starting TLS on a socket that has already carried plaintext.
// CONNECT is exactly that sequence.
//
// It matters most on darwin, where dialTLS uses Network.framework and cannot
// adopt an existing socket. The proxied path goes through Upgrade instead, so
// it works there too -- at Secure Transport's TLS 1.2 ceiling, which is the
// same trade the STARTTLS path already documents.

// dialTLSMaybeProxy connects to host:port, through a proxy when the
// environment names one.
func dialTLSMaybeProxy(ctx context.Context, host, port string, cfg *Config, timeout time.Duration) (net.Conn, error) {
	p, err := proxyFor(host, port, true)
	if err != nil {
		return nil, &Error{Op: "dial", Host: host, Backend: backendName, Err: err}
	}
	if p == nil {
		return dialTLS(ctx, host, port, cfg, timeout)
	}

	timeout = effectiveTimeout(ctx, timeout)
	if timeout <= 0 {
		return nil, &Error{Op: "dial", Host: host, Backend: backendName, Err: errTimeout}
	}

	conn, err := DialPlain(ctx, p.Host, p.Port)
	if err != nil {
		return nil, err
	}
	// The tunnel setup is plaintext HTTP and has to be bounded on its own; the
	// handshake that follows is bounded by ctx inside Upgrade.
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		conn.Close()
		return nil, err
	}
	if err := connectTunnel(conn, host, port, p); err != nil {
		conn.Close()
		return nil, &Error{Op: "dial", Host: host, Backend: backendName, Err: err}
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		conn.Close()
		return nil, err
	}

	// Upgrade takes ownership of the descriptor on success and leaves it alone
	// on failure, so conn is only closed on the error path.
	tlsConn, err := Upgrade(ctx, conn, host, cfg)
	if err != nil {
		conn.Close()
		return nil, err
	}
	return tlsConn, nil
}

// dialPlainMaybeProxy is the http:// counterpart. There is no tunnel here: a
// proxy expects the origin-absolute request form instead, which is why the
// caller is told whether the connection is proxied.
func dialPlainMaybeProxy(ctx context.Context, host, port string, timeout time.Duration) (net.Conn, bool, error) {
	p, err := proxyFor(host, port, false)
	if err != nil {
		return nil, false, &Error{Op: "dial", Host: host, Backend: backendName, Err: err}
	}
	if p == nil {
		conn, err := net.DialTimeout("tcp4", net.JoinHostPort(host, port), timeout)
		return conn, false, err
	}
	conn, err := net.DialTimeout("tcp4", net.JoinHostPort(p.Host, p.Port), timeout)
	if err != nil {
		return nil, false, err
	}
	return conn, true, nil
}

// connectTunnel performs the CONNECT exchange.
//
// The response is read one byte at a time. A buffered reader could pull bytes
// past the header terminator, and those bytes are the first TLS record; losing
// them is the classic in-band upgrade bug.
func connectTunnel(conn net.Conn, host, port string, p *proxy) error {
	target := net.JoinHostPort(host, port)

	var req strings.Builder
	fmt.Fprintf(&req, "CONNECT %s HTTP/1.1\r\n", target)
	fmt.Fprintf(&req, "Host: %s\r\n", target)
	if p.Auth != "" {
		fmt.Fprintf(&req, "Proxy-Authorization: %s\r\n", p.Auth)
	}
	req.WriteString("Proxy-Connection: Keep-Alive\r\n\r\n")

	if _, err := conn.Write([]byte(req.String())); err != nil {
		return fmt.Errorf("https: proxy CONNECT write: %w", err)
	}

	status, err := readTunnelResponse(conn)
	if err != nil {
		return err
	}
	// Any 2xx means the tunnel is open. 407 is worth naming, because it is the
	// one failure a user can fix from the environment.
	switch {
	case strings.HasPrefix(status, "2"):
		return nil
	case strings.HasPrefix(status, "407"):
		return fmt.Errorf("https: proxy requires authentication (%s)", status)
	default:
		return fmt.Errorf("https: proxy refused CONNECT: %s", status)
	}
}

// readTunnelResponse consumes the status line and headers, returning the status
// line's code and reason.
func readTunnelResponse(conn net.Conn) (string, error) {
	var first string
	for i := 0; ; i++ {
		line, err := readCRLFLine(conn)
		if err != nil {
			return "", err
		}
		if i == 0 {
			// "HTTP/1.1 200 Connection established"
			if _, rest, ok := strings.Cut(line, " "); ok {
				first = rest
			} else {
				return "", errors.New("https: malformed proxy response")
			}
		}
		if line == "" {
			return first, nil
		}
		if i > 64 {
			return "", errors.New("https: proxy sent too many header lines")
		}
	}
}

func readCRLFLine(conn net.Conn) (string, error) {
	var sb strings.Builder
	buf := make([]byte, 1)
	for sb.Len() < 8192 {
		n, err := conn.Read(buf)
		if err != nil {
			return "", fmt.Errorf("https: proxy CONNECT read: %w", err)
		}
		if n == 0 {
			continue
		}
		if buf[0] == '\n' {
			return strings.TrimSuffix(sb.String(), "\r"), nil
		}
		sb.WriteByte(buf[0])
	}
	return "", errors.New("https: proxy response line too long")
}
