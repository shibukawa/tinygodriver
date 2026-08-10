#!/usr/bin/env python3
"""Vendor fasthttp/websocket into this directory for the TinyGo build.

Usage:
    python3 vendor.py                            # use the module cache
    python3 vendor.py /path/to/websocket-src     # use an unpacked source tree

Re-run this after bumping WEBSOCKET_VERSION. Every transformation below is also
recorded in PATCHES.md; keep the two in sync. A patch whose anchor text moved
aborts the run rather than half-applying.

Why a fork at all: upstream imports `github.com/valyala/fasthttp`, and Go's
type system makes that final -- an upgrader that takes upstream's
`*fasthttp.RequestCtx` cannot be handed one from the fork at
`github.com/shibukawa/tinygodriver/fasthttp`, because the two types are
distinct even though they are textually identical. That is the same reason
fasthttprouter exists. Unlike the router, this package also needs real patches:
TinyGo's crypto/tls has no Conn type and its net/http has neither
ProxyFromEnvironment nor NewResponseController.

The upstream _test.go files are copied too, as fasthttprouter does. All but one
run under `tinygo test` as vendored; `client_server_test.go` builds real TLS
servers through net/http/httptest and pulls in net/http/cookiejar, which TinyGo
does not ship, so it is constrained to standard Go. The fasthttp side of the
library has no upstream tests at all, so compat_test.go supplies them and runs
on both compilers.

The external dependencies -- klauspost/compress, savsgio/gotils, and
golang.org/x/net for the SOCKS and environment proxy support -- stay ordinary
module requirements and need no fork.
"""

import os
import shutil
import subprocess
import sys
import tempfile
import zipfile

WEBSOCKET_VERSION = "v1.5.12"
WEBSOCKET_MODULE = "github.com/fasthttp/websocket"
FASTHTTP_MODULE = "github.com/valyala/fasthttp"

HERE = os.path.dirname(os.path.abspath(__file__))
LOCAL_MODULE = "github.com/shibukawa/tinygodriver/fasthttpwebsocket"
LOCAL_FASTHTTP = "github.com/shibukawa/tinygodriver/fasthttp"

# The module is one flat package. _examples is a directory of standalone
# programs with its own dependencies and is deliberately absent.
KEEP_DIRS = {""}

# Written by hand, not by this script: clean() must leave them alone.
LOCAL_FILES = {
    "vendor.py",
    "PATCHES.md",
    "README.md",
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
    """Return a directory holding the websocket sources, extracting from the
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
        gomodcache, "cache", "download", WEBSOCKET_MODULE, "@v",
        WEBSOCKET_VERSION + ".zip",
    )
    if not os.path.exists(zip_path):
        fail(
            "module zip not found: %s\nrun: go mod download %s@%s"
            % (zip_path, WEBSOCKET_MODULE, WEBSOCKET_VERSION)
        )

    # Outside HERE on purpose: clean() empties this directory of everything but
    # LOCAL_FILES, and an extraction directory inside it would be the first
    # casualty.
    tmp = tempfile.mkdtemp(prefix="fasthttpwebsocket-vendor-")
    with zipfile.ZipFile(zip_path) as z:
        z.extractall(tmp)
    root = os.path.join(tmp, WEBSOCKET_MODULE + "@" + WEBSOCKET_VERSION)
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
    """Point the library's own imports at this directory and its fasthttp
    imports at the fork. Order matters: the fasthttp replacement must not see
    paths already rewritten to LOCAL_MODULE, and it cannot, because upstream
    never mentions this repository."""
    text = text.replace(WEBSOCKET_MODULE, LOCAL_MODULE)
    return text.replace(FASTHTTP_MODULE, LOCAL_FASTHTTP)


# --------------------------------------------------------------------- patches
#
# Each entry is (old, new, expected_count). An unexpected count aborts, so a
# version bump that moves the anchor fails loudly instead of dropping a patch.
#
# The anchors match upstream's text *after* the import rewrite and *before*
# gofmt, which main() runs last. None of them contains a module path, so the
# rewrite never disturbs them.

PATCHES = {
    "client.go": [
        # TinyGo's http.Transport is an empty struct, so ProxyFromEnvironment
        # does not exist. defaultProxy reads the same variables with the same
        # precedence; see compat_tinygo.go.
        (
            "\tProxy:            http.ProxyFromEnvironment,",
            "\t// PETITWEB: was http.ProxyFromEnvironment; see compat_std.go.\n"
            "\tProxy:            defaultProxy,",
            1,
        ),
        # tls.Client compiles on TinyGo and panics, tls.Conn is not a type
        # there, and ConnectionState is not a method on what net.TLSConn is.
        # The whole block moves behind tlsClientHandshake.
        (
            "\t\ttlsConn := tls.Client(netConn, cfg)\n"
            "\t\tnetConn = tlsConn\n"
            "\n"
            "\t\tif trace != nil && trace.TLSHandshakeStart != nil {\n"
            "\t\t\ttrace.TLSHandshakeStart()\n"
            "\t\t}\n"
            "\t\terr := doHandshake(ctx, tlsConn, cfg)\n"
            "\t\tif trace != nil && trace.TLSHandshakeDone != nil {\n"
            "\t\t\ttrace.TLSHandshakeDone(tlsConn.ConnectionState(), err)\n"
            "\t\t}\n",
            "\t\t// PETITWEB: was tls.Client plus the handshake and its trace calls,\n"
            "\t\t// inline. TinyGo has no tls.Conn type and its tls.Client panics; see\n"
            "\t\t// compat_std.go. netConn is reassigned before the error check either\n"
            "\t\t// way, so the deferred Close above still reaches the connection.\n"
            "\t\ttlsNetConn, err := tlsClientHandshake(ctx, netConn, cfg, trace)\n"
            "\t\tnetConn = tlsNetConn\n",
            1,
        ),
        (
            "func cloneTLSConfig(cfg *tls.Config) *tls.Config {\n"
            "\tif cfg == nil {\n"
            "\t\treturn &tls.Config{}\n"
            "\t}\n"
            "\treturn cfg.Clone()\n"
            "}\n"
            "\n"
            "func doHandshake(ctx context.Context, tlsConn *tls.Conn, cfg *tls.Config) error {\n"
            "\tif err := tlsConn.HandshakeContext(ctx); err != nil {\n"
            "\t\treturn err\n"
            "\t}\n"
            "\tif !cfg.InsecureSkipVerify {\n"
            "\t\tif err := tlsConn.VerifyHostname(cfg.ServerName); err != nil {\n"
            "\t\t\treturn err\n"
            "\t\t}\n"
            "\t}\n"
            "\treturn nil\n"
            "}\n",
            "// PETITWEB: cloneTLSConfig and doHandshake moved to compat_std.go, which has a\n"
            "// TinyGo counterpart. tls.Config there has no Clone method and tls.Conn is not\n"
            "// a type at all.\n",
            1,
        ),
    ],
    # TinyGo has no http.NewResponseController, so the two build constraints are
    # steered to send it down the pre-1.20 Hijacker path instead.
    "server_utils.go": [
        (
            "//go:build go1.20 || go1.21 || go1.22",
            "// PETITWEB: added `&& !tinygo`. TinyGo has no http.NewResponseController, so it\n"
            "// takes the pre-1.20 file's Hijacker path instead; see PATCHES.md.\n"
            "//go:build (go1.20 || go1.21 || go1.22) && !tinygo",
            1,
        ),
    ],
    "server_utils_119.go": [
        (
            "//go:build !go1.20 && !go1.21 && !go1.22",
            "// PETITWEB: added `|| tinygo`; see server_utils.go and PATCHES.md.\n"
            "//go:build (!go1.20 && !go1.21 && !go1.22) || tinygo",
            1,
        ),
    ],
    # net/http/cookiejar is not in TinyGo's standard library, and the file also
    # builds TLS servers through httptest, which needs a tls.Server TinyGo does
    # not define.
    "client_server_test.go": [
        (
            "// license that can be found in the LICENSE file.\n\npackage websocket",
            "// license that can be found in the LICENSE file.\n\n"
            "// PETITWEB: added a build constraint; see PATCHES.md.\n"
            "//go:build !tinygo\n\n"
            "package websocket",
            1,
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
                if not name.endswith(".go"):
                    continue
                out_dir = HERE if rel_dir == "." else os.path.join(HERE, rel_dir)
                os.makedirs(out_dir, exist_ok=True)
                with open(os.path.join(dirpath, name), encoding="utf-8") as f:
                    body = f.read()
                with open(os.path.join(out_dir, name), "w", encoding="utf-8") as f:
                    f.write(rewrite(body))
                n_files += 1

        # BSD-3-Clause requires the notice to travel with the source, and the
        # notice names a group rather than a person.
        for notice in ("LICENSE", "AUTHORS"):
            shutil.copy(os.path.join(src, notice), os.path.join(HERE, notice))

        with open(os.path.join(HERE, "VERSION"), "w", encoding="utf-8") as f:
            f.write(
                "%s %s\nvendored by vendor.py; see PATCHES.md for local changes\n"
                % (WEBSOCKET_MODULE, WEBSOCKET_VERSION)
            )

        apply_patches()

        # Rewriting import paths reorders import groups, and upstream's doc
        # comments predate gofmt's 1.19 rules, so the result is not gofmt-clean
        # either way. A vendored tree that fails gofmt -l is noise in every
        # future diff.
        subprocess.run(["gofmt", "-w", HERE], check=True)

        print("vendored fasthttp/websocket %s: %d sources"
              % (WEBSOCKET_VERSION, n_files))
    finally:
        if tmp:
            shutil.rmtree(tmp, ignore_errors=True)


if __name__ == "__main__":
    main()
