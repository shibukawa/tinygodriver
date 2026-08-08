#!/usr/bin/env python3
"""Vendor fasthttp into this directory for the TinyGo build.

Usage:
    python3 vendor.py                           # use the module cache
    python3 vendor.py /path/to/fasthttp-src     # use an unpacked source tree

Re-run this after bumping FASTHTTP_VERSION. Every transformation below is also
recorded in PATCHES.md; keep the two in sync. A patch whose anchor text moved
aborts the run rather than half-applying.

Why the module is copied instead of patched in place: the changes live inside
upstream's own sources, so there is nothing to wrap from outside. A `replace`
directive would work in this repository and be ignored in anything that imports
it, which is the worst outcome -- consumers would silently get upstream fasthttp
and fail to build under TinyGo. Rewriting the import paths to live here makes
this a real fork with one import path that works on both compilers.

Only the root package plus fasthttputil and stackless are copied: `go list
-deps` shows those are the only ones the root package pulls from its own module.
The external dependencies (brotli, klauspost/compress, bytebufferpool) stay
ordinary module requirements and need no fork.
"""

import os
import re
import shutil
import subprocess
import sys
import tempfile
import zipfile

FASTHTTP_VERSION = "v1.73.0"
FASTHTTP_MODULE = "github.com/valyala/fasthttp"

HERE = os.path.dirname(os.path.abspath(__file__))
LOCAL_MODULE = "github.com/shibukawa/tinygodriver/fasthttp"

# What the root package imports from its own module, measured with `go list
# -deps github.com/valyala/fasthttp`.
KEEP_DIRS = {"", "fasthttputil", "stackless"}

# Written by hand, not by this script: clean() must leave them alone.
LOCAL_FILES = {
    "vendor.py",
    "PATCHES.md",
    "README.md",
    "compat.go",
    "compat_std.go",
    "compat_tinygo.go",
    "compat_test.go",
    "compat_std_test.go",
    "compat_tinygo_test.go",
}


def fail(msg):
    print("vendor.py: " + msg, file=sys.stderr)
    sys.exit(1)


def source_tree():
    """Return a directory holding the fasthttp sources, extracting from the
    module cache when the caller did not supply one."""
    if len(sys.argv) > 1:
        path = os.path.abspath(sys.argv[1])
        if not os.path.isdir(path):
            fail("not a directory: " + path)
        return path, None

    gomodcache = subprocess.run(
        ["go", "env", "GOMODCACHE"], capture_output=True, text=True, check=True
    ).stdout.strip()
    zip_path = os.path.join(
        gomodcache, "cache", "download", FASTHTTP_MODULE, "@v",
        FASTHTTP_VERSION + ".zip",
    )
    if not os.path.exists(zip_path):
        fail(
            "module zip not found: %s\nrun: go mod download %s@%s"
            % (zip_path, FASTHTTP_MODULE, FASTHTTP_VERSION)
        )

    # Outside HERE on purpose: clean() empties this directory of everything but
    # LOCAL_FILES, and an extraction directory inside it would be the first
    # casualty.
    tmp = tempfile.mkdtemp(prefix="fasthttp-vendor-")
    with zipfile.ZipFile(zip_path) as z:
        z.extractall(tmp)
    root = os.path.join(tmp, FASTHTTP_MODULE + "@" + FASTHTTP_VERSION)
    if not os.path.isdir(root):
        fail("unexpected zip layout under " + tmp)
    return root, tmp


def wanted(rel_dir):
    rel_dir = rel_dir.replace(os.sep, "/")
    if rel_dir == ".":
        rel_dir = ""
    return rel_dir in KEEP_DIRS


def clean():
    """Remove the previous vendored tree, keeping the hand-written files."""
    for name in sorted(os.listdir(HERE)):
        if name in LOCAL_FILES:
            continue
        path = os.path.join(HERE, name)
        if os.path.isdir(path):
            shutil.rmtree(path)
        else:
            os.remove(path)


def rewrite(text):
    """Point every fasthttp import at this repository."""
    return text.replace(FASTHTTP_MODULE, LOCAL_MODULE)


# --------------------------------------------------------------------- patches
#
# Each entry is (old, new, expected_count). An unexpected count aborts, so a
# version bump that moves the anchor fails loudly instead of dropping a patch.

PATCHES = {
    "client.go": [
        # TinyGo's crypto/tls has no (*Config).Clone.
        (
            "\t\tc = c.Clone()\n",
            "\t\t// PETITWEB: TinyGo's stub crypto/tls has no (*Config).Clone.\n"
            "\t\tc = cloneTLSConfig(c)\n",
            1,
        ),
        # tls.Client panics under TinyGo, so it moves behind a shim that can
        # report the failure instead.
        (
            "\tconn := tls.Client(rawConn, tlsConfig)\n"
            "\terr := conn.SetDeadline(deadline)\n",
            "\t// PETITWEB: was tls.Client, which panics under TinyGo. Supply a\n"
            "\t// Dial that already returns a TLS connection to serve HTTPS there.\n"
            "\tconn, err := tlsClient(rawConn, tlsConfig)\n"
            "\tif err != nil {\n"
            "\t\treturn nil, err\n"
            "\t}\n"
            "\terr = conn.SetDeadline(deadline)\n",
            1,
        ),
        (
            "\t\tif writeTimeout == 0 {\n"
            "\t\t\treturn tls.Client(conn, tlsConfig), nil\n"
            "\t\t}\n",
            "\t\tif writeTimeout == 0 {\n"
            "\t\t\t// PETITWEB: was tls.Client; see tlsClient.\n"
            "\t\t\treturn tlsClient(conn, tlsConfig)\n"
            "\t\t}\n",
            1,
        ),
    ],
    "server.go": [
        # ConnectionState carries no NegotiatedProtocol on TinyGo.
        (
            "\t\t\treturn tc.ConnectionState().NegotiatedProtocol, nil\n",
            "\t\t\t// PETITWEB: TinyGo's ConnectionState has no NegotiatedProtocol.\n"
            "\t\t\treturn negotiatedProtocol(tc), nil\n",
            1,
        ),
        (
            "\ttlsConfig := s.TLSConfig.Clone()\n",
            "\t// PETITWEB: TinyGo's stub crypto/tls has no (*Config).Clone.\n"
            "\ttlsConfig := cloneTLSConfig(s.TLSConfig)\n",
            2,
        ),
        # TinyGo's tls.NewListener hands back a listener that does no TLS at
        # all, so serving through it would put cleartext on the TLS port.
        (
            "\treturn s.Serve(\n"
            "\t\ttls.NewListener(ln, tlsConfig),\n"
            "\t)\n",
            "\t// PETITWEB: TinyGo's tls.NewListener performs no handshake, so\n"
            "\t// serving through it would emit cleartext on the TLS port.\n"
            "\t// newTLSListener refuses instead of degrading silently.\n"
            "\ttlsLn, err := newTLSListener(ln, tlsConfig)\n"
            "\tif err != nil {\n"
            "\t\treturn err\n"
            "\t}\n"
            "\treturn s.Serve(tlsLn)\n",
            2,
        ),
        # x509KeyPair needs tls.X509KeyPair, which TinyGo does not define at
        # all; only the error text stays shared.
        (
            "func x509KeyPair(certData, keyData []byte) (tls.Certificate, error) {\n"
            "\tif len(certData) == 0 && len(keyData) == 0 {\n"
            "\t\treturn tls.Certificate{}, errNoCertOrKeyProvided\n"
            "\t}\n"
            "\n"
            "\tcert, err := tls.X509KeyPair(certData, keyData)\n"
            "\tif err != nil {\n"
            "\t\treturn tls.Certificate{}, fmt.Errorf(\"cannot load tls key pair from the provided cert data(%d) and key data(%d): %w\",\n"
            "\t\t\tlen(certData), len(keyData), err)\n"
            "\t}\n"
            "\treturn cert, nil\n"
            "}\n",
            "// PETITWEB: the body moved to compat_std.go and compat_tinygo.go,\n"
            "// because TinyGo defines no tls.X509KeyPair. Only the wording stays here.\n"
            "func errCannotLoadTLSKeyPair(certLen, keyLen int, err error) error {\n"
            "\treturn fmt.Errorf(\"cannot load tls key pair from the provided cert data(%d) and key data(%d): %w\",\n"
            "\t\tcertLen, keyLen, err)\n"
            "}\n",
            1,
        ),
    ],
    "peripconn.go": [
        # perIPTLSConn embeds the concrete TLS type on purpose: that is what
        # keeps *perIPTLSConn satisfying the tlsConn interface. An alias
        # preserves it; falling back to net.Conn would not.
        (
            'import (\n\t"crypto/tls"\n\t"net"\n\t"sync"\n)',
            '// PETITWEB: crypto/tls dropped; the TLS type comes from compat_*.go.\nimport (\n\t"net"\n\t"sync"\n)',
            1,
        ),
        (
            "type perIPTLSConn struct {\n\t*tls.Conn\n",
            "type perIPTLSConn struct {\n\t// PETITWEB: *tls.Conn does not exist as a type under TinyGo.\n\t*tlsConnImpl\n",
            1,
        ),
        (
            "\tif tlsConn, ok := conn.(*tls.Conn); ok {",
            "\tif tlsConn, ok := conn.(*tlsConnImpl); ok {",
            1,
        ),
        (
            "\t\t\t\tConn:             tlsConn,",
            "\t\t\t\ttlsConnImpl:      tlsConn,",
            1,
        ),
        ("\t\tc.Conn = tlsConn\n", "\t\tc.tlsConnImpl = tlsConn\n", 1),
        (
            "func (c *perIPTLSConn) Close() error {\n"
            "\tc.lock.Lock()\n"
            "\tcc := c.Conn\n"
            "\tc.Conn = nil\n",
            "func (c *perIPTLSConn) Close() error {\n"
            "\tc.lock.Lock()\n"
            "\tcc := c.tlsConnImpl\n"
            "\tc.tlsConnImpl = nil\n",
            1,
        ),
    ],
    "tcpdialer.go": [
        (
            "\t\tresolver = net.DefaultResolver\n",
            "\t\t// PETITWEB: TinyGo's net defines no Resolver at all.\n"
            "\t\tresolver = defaultResolver()\n",
            1,
        ),
        # netdev resolves names inside Connect, so on TinyGo the address goes to
        # the dialer whole. An explicitly supplied Resolver still wins.
        (
            "\tif d.DisableDNSResolution {\n",
            "\t// PETITWEB: resolveInDialer is true on TinyGo, where name\n"
            "\t// resolution belongs to netdev and there is no Resolver to call.\n"
            "\tif d.DisableDNSResolution || (resolveInDialer && d.Resolver == nil) {\n",
            1,
        ),
    ],
}

# copyZeroAlloc reaches for sendfile through methods TinyGo's os.File and
# net.TCPConn do not have, so the whole function moves into compat_*.go. A
# marker is left behind so a reader of the diff sees that something went.
MOVED_FUNCS = {
    "http.go": [
        (
            r"func copyZeroAlloc\(w io\.Writer, r io\.Reader\) \(int64, error\) \{.*?\n\}\n",
            "// PETITWEB: copyZeroAlloc moved to compat_std.go and compat_tinygo.go.\n"
            "// Its sendfile fast paths need ReadFrom on os.File and net.TCPConn,\n"
            "// which TinyGo implements on neither.\n",
        ),
    ],
}


def apply_patches():
    for name, edits in PATCHES.items():
        path = os.path.join(HERE, name)
        with open(path, encoding="utf-8") as f:
            body = f.read()
        for old, new, count in edits:
            found = body.count(old)
            if found != count:
                fail(
                    "%s: expected %d occurrence(s) of %r, found %d -- upstream "
                    "moved; reconcile with PATCHES.md"
                    % (name, count, old[:60], found)
                )
            body = body.replace(old, new)
        with open(path, "w", encoding="utf-8") as f:
            f.write(body)
        print("patched %s (%d edits)" % (name, len(edits)))

    for name, moves in MOVED_FUNCS.items():
        path = os.path.join(HERE, name)
        with open(path, encoding="utf-8") as f:
            body = f.read()
        for pat, marker in moves:
            m = re.search(pat, body, re.S)
            if not m:
                fail("%s: no match for %r -- reconcile with PATCHES.md" % (name, pat))
            body = body[: m.start()] + marker + body[m.end() :]
        with open(path, "w", encoding="utf-8") as f:
            f.write(body)
        print("moved %d function(s) out of %s" % (len(moves), name))


def main():
    src, tmp = source_tree()
    try:
        clean()

        n_files = 0
        for dirpath, dirnames, filenames in os.walk(src):
            rel_dir = os.path.relpath(dirpath, src)
            if not wanted(rel_dir):
                continue
            for name in sorted(filenames):
                if not name.endswith(".go") or name.endswith("_test.go"):
                    continue
                out_dir = HERE if rel_dir == "." else os.path.join(HERE, rel_dir)
                os.makedirs(out_dir, exist_ok=True)
                with open(os.path.join(dirpath, name), encoding="utf-8") as f:
                    body = f.read()
                with open(os.path.join(out_dir, name), "w", encoding="utf-8") as f:
                    f.write(rewrite(body))
                n_files += 1

        # MIT requires the notice to travel with the source.
        shutil.copy(os.path.join(src, "LICENSE"), os.path.join(HERE, "LICENSE"))

        with open(os.path.join(HERE, "VERSION"), "w", encoding="utf-8") as f:
            f.write(
                "%s %s\nvendored by vendor.py; see PATCHES.md for local changes\n"
                % (FASTHTTP_MODULE, FASTHTTP_VERSION)
            )

        apply_patches()

        # Rewriting import paths reorders import groups, so the result is no
        # longer gofmt-clean. A vendored tree that fails gofmt -l is noise in
        # every future diff.
        subprocess.run(["gofmt", "-w", HERE], check=True)

        print("vendored fasthttp %s: %d sources" % (FASTHTTP_VERSION, n_files))
    finally:
        if tmp:
            shutil.rmtree(tmp, ignore_errors=True)


if __name__ == "__main__":
    main()
