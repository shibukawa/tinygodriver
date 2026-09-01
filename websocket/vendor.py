#!/usr/bin/env python3
"""Vendor gorilla/websocket into this directory for the TinyGo build.

Usage:
    python3 vendor.py                            # use the module cache
    python3 vendor.py /path/to/websocket-src     # use an unpacked source tree

Re-run this after bumping WEBSOCKET_VERSION. Every transformation below is also
recorded in PATCHES.md; keep the two in sync. A patch whose anchor text moved
aborts the run rather than half-applying.

Why a fork at all: four sites in the client's TLS path name symbols TinyGo does
not define, and one of them, tls.Client, panics rather than failing. Upstream
cannot be built for TinyGo without editing those four sites, and applications
name websocket types directly, so a re-export wrapper would break every
release. The fork is the drop-in itself, as in ../fasthttp.

Unlike fasthttp's vendor.py, and like fasthttprouter's, this keeps upstream's
_test.go files: they exercise framing, masking, compression and the close
handshake over net.Pipe and httptest, which is exactly the behaviour a fork
must not change. The few that need a real TLS server are dropped, since
requirement:no-crypto-tls-on-tinygo makes them meaningless here.

Note that gorilla/websocket has no external dependencies -- its SOCKS5 support
is vendored upstream as x_net_proxy.go -- and no assembly, so this fork needs
no module requirements and no noasm tag.
"""

import os
import re
import shutil
import subprocess
import sys
import tempfile
import zipfile

WEBSOCKET_VERSION = "v1.5.3"
WEBSOCKET_MODULE = "github.com/gorilla/websocket"

HERE = os.path.dirname(os.path.abspath(__file__))
LOCAL_MODULE = "github.com/shibukawa/tinygodriver/websocket"

# The module root only. examples/ is a directory of standalone programs with
# their own dependencies and is deliberately absent.
KEEP_DIRS = {""}

# Written by hand, not by this script: clean() must leave them alone.
LOCAL_FILES = {
    "vendor.py",
    "PATCHES.md",
    "README.md",
    "compat_std.go",
    "compat_tinygo.go",
    "compat_test.go",
}

# Upstream sources this fork does not take.
#
# tls_handshake.go and tls_handshake_116.go are folded into clientTLS in the
# compat files: the handshake differs per compiler, and a build-tagged pair
# keyed on the Go version is the wrong axis for that.
#
# The dropped tests need a TLS server or the network. TinyGo can originate TLS
# only through a caller-supplied dialer and can terminate it not at all, so
# these test a configuration this fork does not support.
DROP_FILES = {
    "tls_handshake.go",
    "tls_handshake_116.go",
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
    tmp = tempfile.mkdtemp(prefix="websocket-vendor-")
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
    """Point the package's own imports at this directory. Upstream only
    self-references from its examples and docs, so this is close to a no-op on
    the sources kept here; it exists so a future subpackage cannot slip
    through."""
    return text.replace(WEBSOCKET_MODULE, LOCAL_MODULE)


# --------------------------------------------------------------------- patches
#
# Each entry is (old, new, expected_count). An unexpected count aborts, so a
# version bump that moves the anchor fails loudly instead of dropping a patch.
#
# Four sites, all in client.go, all missing symbols rather than design
# conflicts. The replacements live in compat_std.go and compat_tinygo.go, so
# host Go keeps upstream behaviour exactly.

PATCHES = {
    "client.go": [
        # 1. http.ProxyFromEnvironment: TinyGo's http.Transport is an empty
        # struct and the function does not exist.
        (
            "\tProxy:            http.ProxyFromEnvironment,",
            "\tProxy:            proxyFromEnvironment,",
            1,
        ),
        # 2. tls.Client panics on TinyGo, tls.Conn does not exist there, and
        # ConnectionState is not a method on what a successful dial returns.
        # clientTLS covers all three and, on TinyGo, refuses instead of
        # panicking when the caller supplied no NetDialTLSContext.
        (
            """		tlsConn := tls.Client(netConn, cfg)
		netConn = tlsConn

		if trace != nil && trace.TLSHandshakeStart != nil {
			trace.TLSHandshakeStart()
		}
		err := doHandshake(ctx, tlsConn, cfg)
		if trace != nil && trace.TLSHandshakeDone != nil {
			trace.TLSHandshakeDone(tlsConn.ConnectionState(), err)
		}
""",
            """		if trace != nil && trace.TLSHandshakeStart != nil {
			trace.TLSHandshakeStart()
		}
		// TINYGODRIVER: was tls.Client + doHandshake. See PATCHES.md.
		tlsConn, tlsState, err := clientTLS(ctx, netConn, cfg)
		netConn = tlsConn
		if trace != nil && trace.TLSHandshakeDone != nil {
			trace.TLSHandshakeDone(tlsState, err)
		}
""",
            1,
        ),
        # 3. (*tls.Config).Clone does not exist on TinyGo. The compat files
        # define cloneTLSConfig, so upstream's copy of it must go.
        (
            """func cloneTLSConfig(cfg *tls.Config) *tls.Config {
	if cfg == nil {
		return &tls.Config{}
	}
	return cfg.Clone()
}
""",
            "",
            1,
        ),
    ],
    # 4. A seam so tests can fix the frame mask.
    #
    # Upstream's go.mod says `go 1.12`, so its own test run gets randseednop=0
    # and math/rand.Seed still reseeds the global source newMaskKey draws from.
    # Vendored into a module that says `go 1.27`, Seed is the no-op it became
    # in Go 1.24, and every client case of TestPreparedMessage fails. A
    # //go:debug directive fixes the host build and does nothing under TinyGo,
    # which does not implement godebug. Replacing the source outright is the
    # only fix that works on both compilers.
    #
    # Behaviour is unchanged: the variable is initialised to rand.Uint32, and
    # only a test in this package can reach it.
    "conn.go": [
        (
            """func newMaskKey() [4]byte {
	n := rand.Uint32()""",
            """// TINYGODRIVER: seam for tests, which cannot use rand.Seed. See PATCHES.md.
var maskKeySource = rand.Uint32

func newMaskKey() [4]byte {
	n := maskKeySource()""",
            1,
        ),
    ],
    "prepared_test.go": [
        ("\trand.Seed(1234)", "\tsetFixedMaskKey(t)", 2),
        # rand is now unused in this file.
        ('\t"math/rand"\n', "", 1),
    ],
}

# Upstream tests that cannot run here. Each entry is a file and the reason.
DROP_TESTS = {
    # Needs tls.X509KeyPair and a TLS listener to terminate against, which
    # TinyGo has neither of.
    "client_server_test.go": "terminates TLS; TinyGo defines no tls.Server",
    # Exercises the proxy dialer against live CONNECT proxies it starts itself,
    # through http.ProxyFromEnvironment, which this fork replaces.
    "client_test.go": "asserts on http.ProxyFromEnvironment, replaced here",
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
                if name in DROP_FILES or name in DROP_TESTS:
                    continue
                out_dir = HERE if rel_dir == "." else os.path.join(HERE, rel_dir)
                os.makedirs(out_dir, exist_ok=True)
                with open(os.path.join(dirpath, name), encoding="utf-8") as f:
                    body = f.read()
                with open(os.path.join(out_dir, name), "w", encoding="utf-8") as f:
                    f.write(rewrite(body))
                n_files += 1

        # BSD-2-Clause requires the notice to travel with the source.
        shutil.copy(os.path.join(src, "LICENSE"), os.path.join(HERE, "LICENSE"))
        shutil.copy(os.path.join(src, "AUTHORS"), os.path.join(HERE, "AUTHORS"))

        with open(os.path.join(HERE, "VERSION"), "w", encoding="utf-8") as f:
            f.write(
                "%s %s\nvendored by vendor.py; see PATCHES.md for local changes\n"
                % (WEBSOCKET_MODULE, WEBSOCKET_VERSION)
            )

        apply_patches()

        # Rewriting import paths reorders import groups, so the result is no
        # longer gofmt-clean. A vendored tree that fails gofmt -l is noise in
        # every future diff.
        subprocess.run(["gofmt", "-w", HERE], check=True)

        print("vendored gorilla/websocket %s: %d sources" % (WEBSOCKET_VERSION, n_files))
    finally:
        if tmp:
            shutil.rmtree(tmp, ignore_errors=True)


if __name__ == "__main__":
    main()
