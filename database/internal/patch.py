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
// database/pgx installs as Config.DialFunc: TinyGo cannot recover a
// descriptor from an arbitrary net.Conn, so the dialer has to supply one that
// carries its own.
func upgradeConn(ctx context.Context, conn net.Conn, cfg *TLSConfig) (net.Conn, error) {
	if cfg == nil {
		return nil, ErrTLSUnsupported
	}
	return https.Upgrade(ctx, conn, cfg.Host, toHTTPSConfig(cfg))
}

// toHTTPSConfig is the single adapter between the vendored pgx configuration
// shape and the repository's backend-neutral TLS configuration.
func toHTTPSConfig(cfg *TLSConfig) *https.Config {
	if cfg == nil {
		return nil
	}
	return &https.Config{
		RootCAs:            cfg.RootCAsPEM,
		RootCAsOnly:        cfg.RootCAsOnly,
		InsecureSkipVerify: cfg.InsecureSkipVerify,
		ServerName:         cfg.ServerName,
	}
}

// hostResolver defers name resolution to the dialer. TinyGo has no
// net.Resolver, and netdev resolves the host inside Connect anyway.
type hostResolver struct{}

func (hostResolver) LookupHost(ctx context.Context, host string) ([]string, error) {
	return []string{host}, nil
}
'''

# The adapter's field-inventory test (decision:pgx-tls-config-boundary) is
# repository-authored; vendor.py drops upstream tests, so write it here too.
TLS_SHIM_TEST = '''package pgconn

import (
	"reflect"
	"slices"
	"testing"

	"github.com/shibukawa/tinygodriver/https"
)

func TestHTTPSConfigAdapter(t *testing.T) {
	pem := []byte("pem")
	in := &TLSConfig{
		Host:               "db.example",
		ServerName:         "verify.example",
		RootCAsPEM:         [][]byte{pem},
		RootCAsOnly:        true,
		InsecureSkipVerify: true,
	}
	out := toHTTPSConfig(in)
	if out.ServerName != in.ServerName || out.RootCAsOnly != in.RootCAsOnly ||
		out.InsecureSkipVerify != in.InsecureSkipVerify ||
		len(out.RootCAs) != 1 || !slices.Equal(out.RootCAs[0], pem) {
		t.Fatalf("adapter output = %#v, input = %#v", out, in)
	}
	if out.Certificates != nil || out.MinVersion != 0 {
		t.Fatal("adapter populated intentionally unsupported HTTPS fields")
	}
}

func TestHTTPSConfigAdapterFieldInventory(t *testing.T) {
	assertExportedFields(t, reflect.TypeFor[TLSConfig](), []string{
		"Host", "ServerName", "RootCAsPEM", "RootCAsOnly", "InsecureSkipVerify",
	})
	assertExportedFields(t, reflect.TypeFor[https.Config](), []string{
		"RootCAs", "RootCAsOnly", "Certificates", "InsecureSkipVerify", "ServerName", "MinVersion",
	})
}

func assertExportedFields(t *testing.T, typ reflect.Type, want []string) {
	t.Helper()
	var got []string
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if field.IsExported() {
			got = append(got, field.Name)
		}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("%s exported fields = %v, want %v; update the adapter and inventory", typ, got, want)
	}
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

    base64_import = '\t"encoding/base64"\n'
    require(body, base64_import, "auth_scram.go")
    body = body.replace(base64_import, "")
    precis_import = '\t"golang.org/x/text/secure/precis"\n'
    require(body, precis_import, "auth_scram.go")
    body = body.replace(
        precis_import,
        '\tbase64 "github.com/shibukawa/tinygodriver/encoding/simdbase64" // Local patch: shared SIMD Base64 backend.\n'
        + precis_import,
    )

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


REDACT_HELPERS = '''
// Local patch: string scans instead of regexps; see ../../PATCHES.md.

// redactKVQuoted replaces every `password='[^']*'`.
func redactKVQuoted(s string) string {
	var b strings.Builder
	for {
		i := strings.Index(s, "password='")
		if i < 0 {
			b.WriteString(s)
			break
		}
		rest := s[i+len("password='"):]
		j := strings.IndexByte(rest, '\\'')
		if j < 0 {
			// No closing quote anywhere ahead, so no further match is possible.
			b.WriteString(s)
			break
		}
		b.WriteString(s[:i])
		b.WriteString("password=xxxxx")
		s = rest[j+1:]
	}
	return b.String()
}

// redactKVPlain replaces every `password=[^ ]*`.
func redactKVPlain(s string) string {
	var b strings.Builder
	for {
		i := strings.Index(s, "password=")
		if i < 0 {
			b.WriteString(s)
			break
		}
		b.WriteString(s[:i])
		b.WriteString("password=xxxxx")
		rest := s[i+len("password="):]
		j := strings.IndexByte(rest, ' ')
		if j < 0 {
			break
		}
		s = rest[j:]
	}
	return b.String()
}

// redactColonPassword replaces every `:[^:@]+?@`, the password position of a
// URL too mangled for url.Parse.
func redactColonPassword(s string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		if s[i] != ':' {
			b.WriteByte(s[i])
			i++
			continue
		}
		j := i + 1
		for j < len(s) && s[j] != ':' && s[j] != '@' {
			j++
		}
		if j > i+1 && j < len(s) && s[j] == '@' {
			b.WriteString(":xxxxxx@")
			i = j + 1
		} else {
			b.WriteByte(':')
			i++
		}
	}
	return b.String()
}

// redactBrokenUserinfo replaces every `:[^@]+@`, the stranded password of a
// URI whose userinfo the parser could not delimit.
func redactBrokenUserinfo(s string) string {
	var b strings.Builder
	for {
		i := strings.IndexByte(s, ':')
		if i < 0 {
			b.WriteString(s)
			break
		}
		j := strings.IndexByte(s[i+1:], '@')
		if j < 0 {
			// No '@' after this ':', so none after any later ':' either.
			b.WriteString(s)
			break
		}
		if j == 0 {
			// ":@" has nothing to mask, and the '@' cannot start a match.
			b.WriteString(s[:i+2])
			s = s[i+2:]
			continue
		}
		b.WriteString(s[:i])
		b.WriteString(":xxxxxx@")
		s = s[i+1+j+1:]
	}
	return b.String()
}
'''


PGPASS_LOCAL = '''package pgconn

// Local patch: see ../../PATCHES.md.
//
// A package-local .pgpass reader replacing github.com/jackc/pgpassfile. That
// module keeps an unused package-level regexp.MustCompile, which alone links
// the whole regexp engine into every TinyGo binary; the parser itself is the
// few lines below, behavior-identical to upstream.

import (
	"bufio"
	"io"
	"os"
	"strings"
)

type pgPassfile struct {
	entries []pgPassEntry
}

type pgPassEntry struct {
	hostname string
	port     string
	database string
	username string
	password string
}

// readPassfile reads the file at path, matching pgpassfile.ReadPassfile.
func readPassfile(path string) (*pgPassfile, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return parsePassfile(f)
}

func parsePassfile(r io.Reader) (*pgPassfile, error) {
	pf := &pgPassfile{}
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		if entry, ok := parsePassfileLine(scanner.Text()); ok {
			pf.entries = append(pf.entries, entry)
		}
	}
	return pf, scanner.Err()
}

// parsePassfileLine parses one line. Comments and unparsable lines are
// skipped, as upstream skips them.
func parsePassfileLine(line string) (pgPassEntry, bool) {
	const (
		tmpBackslash = "\\r"
		tmpColon     = "\\n"
	)
	line = strings.TrimSpace(line)
	if strings.HasPrefix(line, "#") {
		return pgPassEntry{}, false
	}
	line = strings.ReplaceAll(line, `\\\\`, tmpBackslash)
	line = strings.ReplaceAll(line, `\\:`, tmpColon)
	parts := strings.Split(line, ":")
	if len(parts) != 5 {
		return pgPassEntry{}, false
	}
	for i := range parts {
		parts[i] = strings.ReplaceAll(parts[i], tmpBackslash, `\\`)
		parts[i] = strings.ReplaceAll(parts[i], tmpColon, `:`)
	}
	return pgPassEntry{
		hostname: parts[0], port: parts[1], database: parts[2],
		username: parts[3], password: parts[4],
	}, true
}

// FindPassword matches pgpassfile's method of the same name, so the call in
// config.go keeps its shape.
func (pf *pgPassfile) FindPassword(hostname, port, database, username string) string {
	for _, e := range pf.entries {
		if (e.hostname == "*" || e.hostname == hostname) &&
			(e.port == "*" || e.port == port) &&
			(e.database == "*" || e.database == database) &&
			(e.username == "*" || e.username == username) {
			return e.password
		}
	}
	return ""
}
'''


def patch_pgpass():
    """Replace the external pgpassfile dependency with a package-local parser,
    dropping its (unused but linked) package-level regexp."""
    write(os.path.join(PGCONN, "pgpass_local.go"), PGPASS_LOCAL)

    path = os.path.join(PGCONN, "config.go")
    body = read(path)
    old = "\tpassfile, err := pgpassfile.ReadPassfile(settings[\"passfile\"])\n"
    require(body, old, "config.go")
    body = body.replace(old, "\tpassfile, err := readPassfile(settings[\"passfile\"])\n")
    require(body, '\t"github.com/jackc/pgpassfile"\n', "config.go")
    body = body.replace('\t"github.com/jackc/pgpassfile"\n', "")
    write(path, body)


def patch_errors():
    """Drop regexp from pgconn/errors.go: redactPW's three patterns and
    redactURLPassword's one become string scans, verified equivalent against
    the originals over fixed cases and 200k random inputs."""
    path = os.path.join(PGCONN, "errors.go")
    body = read(path)

    old = (
        "\tquotedKV := regexp.MustCompile(`password='[^']*'`)\n"
        '\tconnString = quotedKV.ReplaceAllLiteralString(connString, "password=xxxxx")\n'
        "\tplainKV := regexp.MustCompile(`password=[^ ]*`)\n"
        '\tconnString = plainKV.ReplaceAllLiteralString(connString, "password=xxxxx")\n'
        "\tbrokenURL := regexp.MustCompile(`:[^:@]+?@`)\n"
        '\tconnString = brokenURL.ReplaceAllLiteralString(connString, ":xxxxxx@")\n'
        "\treturn connString\n"
        "}\n"
    )
    require(body, old, "errors.go")
    body = body.replace(
        old,
        "\t// Local patch: string scans instead of regexps; see PATCHES.md. Each\n"
        "\t// helper reproduces the replacement of the pattern named in its comment.\n"
        "\tconnString = redactKVQuoted(connString)\n"
        "\tconnString = redactKVPlain(connString)\n"
        "\tconnString = redactColonPassword(connString)\n"
        "\treturn connString\n"
        "}\n" + REDACT_HELPERS,
    )

    # v5.11.0 added a fourth pattern, for URIs too malformed to delimit.
    old = (
        "\t\tbrokenUserinfo := regexp.MustCompile(`:[^@]+@`)\n"
        '\t\tp = brokenUserinfo.ReplaceAllLiteralString(p, ":xxxxxx@")\n'
    )
    require(body, old, "errors.go")
    body = body.replace(
        old,
        "\t\t// Local patch: a string scan instead of a regexp; see PATCHES.md.\n"
        "\t\tp = redactBrokenUserinfo(p)\n",
    )
    require(body, '\t"regexp"\n', "errors.go")
    body = body.replace('\t"regexp"\n', "")
    write(path, body)


def patch_derived_types():
    """Drop regexp from derived_types.go: the digit prefix of server_version
    needs no compiled pattern, and upstream recompiled it per call."""
    path = os.path.join(HERE, "pgx", "derived_types.go")
    body = read(path)

    old = "\tserverVersionStr = regexp.MustCompile(`^[0-9]+`).FindString(serverVersionStr)\n"
    require(body, old, "derived_types.go")
    body = body.replace(
        old,
        "\t// Local patch: a digit-prefix scan instead of a regexp; see PATCHES.md.\n"
        "\tdigits := 0\n"
        "\tfor digits < len(serverVersionStr) && serverVersionStr[digits] >= '0' && serverVersionStr[digits] <= '9' {\n"
        "\t\tdigits++\n"
        "\t}\n"
        "\tserverVersionStr = serverVersionStr[:digits]\n",
    )
    require(body, '\t"regexp"\n', "derived_types.go")
    body = body.replace('\t"regexp"\n', "")
    write(path, body)


BYTEA_HEX_TEST = r'''package pgtype

import (
	"bytes"
	std "encoding/hex"
	"errors"
	"testing"
)

type hexTestBytesValuer struct {
	value []byte
	err   error
}

func (v hexTestBytesValuer) BytesValue() ([]byte, error) { return v.value, v.err }

func TestByteaTextHexBuffers(t *testing.T) {
	for _, data := range [][]byte{nil, {}, {0, 0xff, 0x10, 0x7f}, bytes.Repeat([]byte{0, 0x9a, 0xbc, 0xff}, 1024)} {
		want := append([]byte("prefix:\\x"), std.EncodeToString(data)...)
		if data == nil {
			want = nil
		}
		for _, valuer := range []bool{false, true} {
			buf := make([]byte, len("prefix:"), len(want)+16)
			copy(buf, "prefix:")
			var got []byte
			var err error
			if valuer {
				got, err = (encodePlanBytesCodecTextBytesValuer{}).Encode(hexTestBytesValuer{value: data}, buf)
			} else {
				got, err = (encodePlanBytesCodecTextBytes{}).Encode(data, buf)
			}
			if err != nil || !bytes.Equal(got, want) || (got == nil) != (want == nil) {
				t.Fatalf("valuer=%v size=%d: incorrect prefix, nil state or bytea encoding: %v", valuer, len(data), err)
				return
			}
		}
	}
	wantErr := errors.New("valuer failure")
	got, err := (encodePlanBytesCodecTextBytesValuer{}).Encode(hexTestBytesValuer{err: wantErr}, []byte("prefix:"))
	if err != wantErr || got != nil {
		t.Fatal("valuer error was not preserved")
	}
}
'''


def patch_hex():
    """Use the shared hex backend and append bytea directly into its buffer."""
    pgx_root = os.path.join(HERE, "pgx")
    old_import = '\t"encoding/hex"\n'
    local_import = '\n\thex "github.com/shibukawa/tinygodriver/encoding/simdhex" // Local patch: shared SIMD hex backend.'
    patched = 0
    for root, dirs, files in os.walk(pgx_root):
        dirs.sort()
        for name in sorted(files):
            if not name.endswith(".go") or name.endswith("_test.go"):
                continue
            path = os.path.join(root, name)
            body = read(path)
            if old_import not in body:
                continue
            body = body.replace(old_import, "", 1)
            begin = body.index("import (")
            end = body.index("\n)", begin)
            local = body.find('\n\t"github.com/shibukawa/tinygodriver/', begin, end)
            if local < 0:
                body = body[:end] + "\n" + local_import + body[end:]
            else:
                body = body[:local] + local_import + body[local:]
            write(path, body)
            patched += 1
    if not patched:
        sys.exit("patch.py: no upstream hex imports found")

    path = os.path.join(pgx_root, "pgtype", "bytea.go")
    body = read(path)
    old = "\tbuf = append(buf, hex.EncodeToString(b)...)\n"
    require(body, old, "pgtype/bytea.go")
    if body.count(old) != 2:
        sys.exit("patch.py: expected two bytea text encoders")
    write(path, body.replace(old, "\tbuf = hex.AppendEncode(buf, b)\n"))
    write(os.path.join(pgx_root, "pgtype", "bytea_hex_test.go"), BYTEA_HEX_TEST)


def main():
    if not os.path.isdir(PGCONN):
        sys.exit("patch.py: run vendor.py first")

    write(os.path.join(PGCONN, "tinygo_tls.go"), TLS_SHIM)
    write(os.path.join(PGCONN, "tinygo_tls_test.go"), TLS_SHIM_TEST)
    patch_config()
    patch_pgconn()
    patch_auth_scram()
    patch_errors()
    patch_derived_types()
    patch_pgpass()
    patch_hex()

    # Removing imports and swapping types leaves the files unformatted, and a
    # vendored tree that fails gofmt -l is noise in every future diff.
    subprocess.run(["gofmt", "-w", os.path.join(HERE, "pgx")], check=True)
    print("applied the patches in PATCHES.md")


if __name__ == "__main__":
    main()
