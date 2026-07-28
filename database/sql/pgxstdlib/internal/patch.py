#!/usr/bin/env python3
"""Apply the local patches recorded in PATCHES.md to the vendored pgx.

Usage:
    python3 vendor.py && python3 patch.py

Every edit is anchored on an exact upstream string and fails loudly if that
string is gone, so a version bump reports which hunk needs attention instead of
silently producing a tree that still links crypto/tls.
"""

import os
import subprocess
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
PGCONN = os.path.join(HERE, "pgx", "pgconn")

# A new file rather than edits scattered through config.go: it keeps the
# replacement surface in one reviewable place, and keeps the diff against
# upstream small.
TLS_SHIM = '''package pgconn

// Local patch: see ../../PATCHES.md
//
// TinyGo ships crypto/tls as a stub, so this build cannot use it. TLS instead
// goes through the https package, which starts TLS on an already-connected
// socket using the platform's native stack. That is exactly the shape
// PostgreSQL needs, because SSLRequest is negotiated in plaintext first.

import (
	"context"
	"errors"
	"net"

	"github.com/shibukawa/tinygodriver/https"
)

// ErrTLSUnsupported reports that TLS could not be started. Refusing is the only
// safe answer: falling back to plaintext would hand an unencrypted connection
// to a caller who asked for an encrypted one.
var ErrTLSUnsupported = errors.New("pgconn: TLS is not supported on this platform")

// TLSConfig stands in for *tls.Config so Config keeps its shape without linking
// crypto/tls. It carries what the https backends can act on.
//
// Client certificates are absent because the native backends cannot offer one;
// configTLS rejects sslcert and sslkey rather than ignoring them.
type TLSConfig struct {
	// Host is the name to verify the certificate against.
	Host string

	// ServerName overrides Host for SNI. Empty when the connection targets a
	// literal IP, per RFC 6066.
	ServerName string

	// RootCAsPEM are additional trust anchors read from sslrootcert.
	RootCAsPEM [][]byte

	// RootCAsOnly ignores the system trust store.
	RootCAsOnly bool

	// InsecureSkipVerify disables verification, for the sslmode values that
	// encrypt without authenticating.
	InsecureSkipVerify bool
}

// Clone matches the tls.Config method pgconn calls when copying fallbacks.
func (c *TLSConfig) Clone() *TLSConfig {
	if c == nil {
		return nil
	}
	d := *c
	return &d
}

// upgradeConn starts TLS on a connection that has already carried plaintext.
//
// The connection must come from https.DialPlain, which is what
// database/sql/pgxstdlib installs as Config.DialFunc: TinyGo cannot recover a
// descriptor from an arbitrary net.Conn, so the dialer has to supply one that
// carries its own.
func upgradeConn(ctx context.Context, conn net.Conn, cfg *TLSConfig) (net.Conn, error) {
	if cfg == nil {
		return nil, ErrTLSUnsupported
	}
	return https.Upgrade(ctx, conn, cfg.Host, &https.Config{
		RootCAs:            cfg.RootCAsPEM,
		RootCAsOnly:        cfg.RootCAsOnly,
		InsecureSkipVerify: cfg.InsecureSkipVerify,
		ServerName:         cfg.ServerName,
	})
}

// hostResolver defers name resolution to the dialer. TinyGo has no
// net.Resolver, and netdev resolves the host inside Connect anyway.
type hostResolver struct{}

func (hostResolver) LookupHost(ctx context.Context, host string) ([]string, error) {
	return []string{host}, nil
}
'''

# Replaces upstream configTLS. The sslmode matrix follows libpq, with the two
# deliberate differences recorded in PATCHES.md.
CONFIG_TLS = '''func configTLS(settings map[string]string, thisHost string, parseConfigOptions ParseConfigOptions) ([]*TLSConfig, error) {
	host := thisHost
	sslmode := settings["sslmode"]
	sslrootcert := settings["sslrootcert"]
	sslcert := settings["sslcert"]
	sslkey := settings["sslkey"]
	sslsni := settings["sslsni"]
	sslnegotiation := settings["sslnegotiation"]

	// Match libpq defaults.
	if sslmode == "" {
		sslmode = "prefer"
	}
	if sslsni == "" {
		sslsni = "1"
	}
	if sslnegotiation == "direct" && sslmode == "prefer" {
		sslmode = "require"
	}

	if sslcert != "" || sslkey != "" {
		return nil, errors.New("pgconn: client certificates are not supported in this build")
	}

	cfg := &TLSConfig{Host: host}

	if sslrootcert != "" {
		if sslrootcert == "system" {
			// libpq treats an explicit system store as a request for full
			// verification.
			sslmode = "verify-full"
		} else {
			pemBytes, err := os.ReadFile(sslrootcert)
			if err != nil {
				return nil, fmt.Errorf("unable to read CA file: %w", err)
			}
			cfg.RootCAsPEM = [][]byte{pemBytes}
			cfg.RootCAsOnly = true
		}
	}

	switch sslmode {
	case "disable":
		return []*TLSConfig{nil}, nil
	case "allow", "prefer":
		// Encrypt opportunistically, without authenticating the server.
		cfg.InsecureSkipVerify = true
	case "require":
		// Per the PostgreSQL docs, a configured root certificate makes
		// sslmode=require behave like verify-ca.
		if sslrootcert == "" {
			cfg.InsecureSkipVerify = true
		}
	case "verify-ca", "verify-full":
		// verify-ca is treated as verify-full: libpq would skip the hostname
		// check, which the native backends cannot express. Checking the name
		// as well is stricter, never weaker, so it is the safe direction in
		// which to differ. Recorded in PATCHES.md.
	default:
		return nil, errors.New("sslmode is invalid")
	}

	// Per RFC 6066 SNI is not sent for a literal IP address.
	if sslsni == "1" && net.ParseIP(host) == nil {
		cfg.ServerName = host
	}

	switch sslmode {
	case "allow":
		return []*TLSConfig{nil, cfg}, nil
	case "prefer":
		return []*TLSConfig{cfg, nil}, nil
	default:
		return []*TLSConfig{cfg}, nil
	}
}
'''


def read(path):
    with open(path, encoding="utf-8") as f:
        return f.read()


def write(path, body):
    with open(path, "w", encoding="utf-8") as f:
        f.write(body)


def require(body, anchor, where):
    if anchor not in body:
        sys.exit(
            "patch.py: anchor missing in %s, reconcile PATCHES.md:\n  %r"
            % (where, anchor[:100])
        )


def replace_func(body, signature, replacement):
    """Swap a whole top-level function, located by its signature."""
    start = body.index(signature)
    tail = body.find("\nfunc ", start + len(signature))
    rest = "" if tail == -1 else body[tail + 1 :]
    return body[:start] + replacement + rest


def patch_config():
    path = os.path.join(PGCONN, "config.go")
    body = read(path)
    require(body, "func configTLS(", "config.go")

    body = replace_func(body, "func configTLS(", CONFIG_TLS)

    for dead in ('\t"crypto/tls"\n', '\t"crypto/x509"\n', '\t"encoding/pem"\n'):
        body = body.replace(dead, "")
    body = body.replace("[]*tls.Config", "[]*TLSConfig")
    body = body.replace("*tls.Config", "*TLSConfig")
    body = body.replace("*net.Resolver", "hostResolver")
    body = body.replace(
        "func makeDefaultResolver() hostResolver {\n\treturn net.DefaultResolver\n}",
        "func makeDefaultResolver() hostResolver { return hostResolver{} }",
    )
    write(path, body)


def patch_pgconn():
    path = os.path.join(PGCONN, "pgconn.go")
    body = read(path)

    edits = [
        # startTLS gains a context so the handshake is bounded like every other
        # network operation. Its SSLRequest exchange is untouched.
        (
            "func startTLS(conn net.Conn, tlsConfig *tls.Config) (net.Conn, error) {",
            "func startTLS(ctx context.Context, conn net.Conn, tlsConfig *TLSConfig) (net.Conn, error) {",
        ),
        ("\treturn tls.Client(conn, tlsConfig), nil", "\treturn upgradeConn(ctx, conn, tlsConfig)"),
        # Connect path.
        (
            "tlsConn = tls.Client(pgConn.conn, connectConfig.tlsConfig)",
            "tlsConn, err = upgradeConn(ctx, pgConn.conn, connectConfig.tlsConfig)",
        ),
        (
            "tlsConn, err = startTLS(pgConn.conn, connectConfig.tlsConfig)",
            "tlsConn, err = startTLS(ctx, pgConn.conn, connectConfig.tlsConfig)",
        ),
        # Cancel-request path. Upstream ignores the error in the direct branch;
        # this build checks it, since upgradeConn can fail where tls.Client
        # could not.
        (
            "tlsCancelConn = tls.Client(cancelConn, pgConn.tlsConfig)",
            "tlsCancelConn, err = upgradeConn(ctx, cancelConn, pgConn.tlsConfig)\n"
            "\t\t\tif err != nil {\n"
            '\t\t\t\treturn fmt.Errorf("tls error on cancel connection: %w", err)\n'
            "\t\t\t}",
        ),
        (
            "tlsCancelConn, err = startTLS(cancelConn, pgConn.tlsConfig)",
            "tlsCancelConn, err = startTLS(ctx, cancelConn, pgConn.tlsConfig)",
        ),
        ('\t"crypto/tls"\n', ""),
        ("*tls.Config", "*TLSConfig"),
    ]
    for old, new in edits:
        require(body, old, "pgconn.go")
        body = body.replace(old, new)
    write(path, body)


def patch_auth_scram():
    path = os.path.join(PGCONN, "auth_scram.go")
    body = read(path)
    require(body, "func getTLSCertificateHash(", "auth_scram.go")

    body = replace_func(
        body,
        "func getTLSCertificateHash(",
        "func getTLSCertificateHash(conn tlsConnLike) ([]byte, error) {\n"
        '\treturn nil, errors.New("pgconn: channel binding is not supported in this build")\n'
        "}\n",
    )
    body = body.replace(
        'if tlsConn, ok := c.conn.(*tls.Conn); ok && c.config.ChannelBinding != "disable" {',
        'if tlsConn, ok := c.conn.(tlsConnLike); ok && c.config.ChannelBinding != "disable" {',
    )
    # No type satisfies this, so channel binding is statically unreachable.
    # tls-server-end-point needs the peer certificate, which the native TLS
    # backends do not surface.
    body = body.replace(
        "func getTLSCertificateHash(",
        "type tlsConnLike interface{ neverImplementedWithoutCryptoTLS() }\n\n"
        "func getTLSCertificateHash(",
    )
    for dead in (
        '\t"crypto/tls"\n',
        '\t"crypto/x509"\n',
        '\t"crypto/sha512"\n',
        '\t"hash"\n',
    ):
        body = body.replace(dead, "")
    write(path, body)


def main():
    if not os.path.isdir(PGCONN):
        sys.exit("patch.py: run vendor.py first")

    write(os.path.join(PGCONN, "tinygo_tls.go"), TLS_SHIM)
    patch_config()
    patch_pgconn()
    patch_auth_scram()

    # Removing imports and swapping types leaves the files unformatted, and a
    # vendored tree that fails gofmt -l is noise in every future diff.
    subprocess.run(["gofmt", "-w", PGCONN], check=True)
    print("applied the patches in PATCHES.md")


if __name__ == "__main__":
    main()
