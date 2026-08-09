#!/usr/bin/env python3
"""Vendor fasthttp/router into this directory for the TinyGo build.

Usage:
    python3 vendor.py                         # use the module cache
    python3 vendor.py /path/to/router-src     # use an unpacked source tree

Re-run this after bumping ROUTER_VERSION. Every transformation below is also
recorded in PATCHES.md; keep the two in sync. A patch whose anchor text moved
aborts the run rather than half-applying.

Why a fork at all: upstream imports `github.com/valyala/fasthttp`, and Go's
type system makes that final -- a router registering handlers against upstream
fasthttp cannot serve requests through the fork at
`github.com/shibukawa/tinygodriver/fasthttp`, because the two RequestHandler
types are distinct even though they are textually identical. Rewriting the
import is the entire point of this fork; see PATCHES.md for anything beyond it.

Unlike fasthttp's vendor.py this one copies the upstream _test.go files too.
fasthttp's test suite leans on crypto/tls and real sockets and was replaced by
a hand-written battery; the router's tests are pure routing logic that runs
under `tinygo test` as-is, and keeping them is the cheapest proof of parity.

The external dependencies (savsgio/gotils, bytebufferpool) stay ordinary
module requirements and need no fork.
"""

import os
import re
import shutil
import subprocess
import sys
import tempfile
import zipfile

ROUTER_VERSION = "v1.5.4"
ROUTER_MODULE = "github.com/fasthttp/router"
FASTHTTP_MODULE = "github.com/valyala/fasthttp"

HERE = os.path.dirname(os.path.abspath(__file__))
LOCAL_MODULE = "github.com/shibukawa/tinygodriver/fasthttprouter"
LOCAL_FASTHTTP = "github.com/shibukawa/tinygodriver/fasthttp"

# The module root plus its only subpackage. _examples is a directory of
# standalone programs with its own dependencies and is deliberately absent.
KEEP_DIRS = {"", "radix"}

# Written by hand, not by this script: clean() must leave them alone.
LOCAL_FILES = {
    "vendor.py",
    "PATCHES.md",
    "README.md",
    "handleridentity_std_test.go",
    "handleridentity_tinygo_test.go",
}


def fail(msg):
    print("vendor.py: " + msg, file=sys.stderr)
    sys.exit(1)


def source_tree():
    """Return a directory holding the router sources, extracting from the
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
        gomodcache, "cache", "download", ROUTER_MODULE, "@v",
        ROUTER_VERSION + ".zip",
    )
    if not os.path.exists(zip_path):
        fail(
            "module zip not found: %s\nrun: go mod download %s@%s"
            % (zip_path, ROUTER_MODULE, ROUTER_VERSION)
        )

    # Outside HERE on purpose: clean() empties this directory of everything but
    # LOCAL_FILES, and an extraction directory inside it would be the first
    # casualty.
    tmp = tempfile.mkdtemp(prefix="fasthttprouter-vendor-")
    with zipfile.ZipFile(zip_path) as z:
        z.extractall(tmp)
    root = os.path.join(tmp, ROUTER_MODULE + "@" + ROUTER_VERSION)
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
    """Point the router's own imports at this directory and its fasthttp
    imports at the fork. Order matters: the fasthttp replacement must not see
    paths already rewritten to LOCAL_MODULE, and it cannot, because upstream
    never mentions this repository."""
    text = text.replace(ROUTER_MODULE, LOCAL_MODULE)
    return text.replace(FASTHTTP_MODULE, LOCAL_FASTHTTP)


# --------------------------------------------------------------------- patches
#
# Each entry is (old, new, expected_count). An unexpected count aborts, so a
# version bump that moves the anchor fails loudly instead of dropping a patch.
#
# The non-test sources carry no patches at all: they touch none of the symbols
# TinyGo is missing (no crypto/tls, no os.File fast paths, no net), so the
# import rewrite is the whole fork there. The only patches are to the tests,
# which upstream wrote against fasthttp v1.58; v1.73 enforces the Host header
# on HTTP/1.1 requests, so every raw request the tests write needs one. The
# strings below are Go source escapes, not real CR LF bytes.

PATCHES = {
    "router_test.go": [
        (
            r" HTTP/1.1\r\n\r\n",
            r" HTTP/1.1\r\nHost: test\r\n\r\n",
            3,
        ),
        # TinyGo's reflect.Value.Pointer() is unreliable for funcs: a handler
        # looked up from the tree calls the right function yet reflects to a
        # different pointer. sameHandler (handleridentity_test.go) compares the
        # raw func-value words instead, which both compilers define.
        (
            "if reflect.ValueOf(h).Pointer() != reflect.ValueOf(handler1).Pointer() {",
            "// PETITWEB: was a reflect.Value.Pointer comparison; see handleridentity_test.go.\n"
            "\t\t\tif !sameHandler(h, handler1) {",
            1,
        ),
        (
            "if reflect.ValueOf(h).Pointer() != reflect.ValueOf(handler2).Pointer() {",
            "// PETITWEB: was a reflect.Value.Pointer comparison; see handleridentity_test.go.\n"
            "\t\t\tif !sameHandler(h, handler2) {",
            1,
        ),
    ],
    "group_test.go": [
        (
            r" HTTP/1.1\r\n\r\n",
            r" HTTP/1.1\r\nHost: test\r\n\r\n",
            10,
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

        # BSD-3-Clause requires the notice to travel with the source.
        shutil.copy(os.path.join(src, "LICENSE"), os.path.join(HERE, "LICENSE"))

        with open(os.path.join(HERE, "VERSION"), "w", encoding="utf-8") as f:
            f.write(
                "%s %s\nvendored by vendor.py; see PATCHES.md for local changes\n"
                % (ROUTER_MODULE, ROUTER_VERSION)
            )

        apply_patches()

        # Rewriting import paths reorders import groups, so the result is no
        # longer gofmt-clean. A vendored tree that fails gofmt -l is noise in
        # every future diff.
        subprocess.run(["gofmt", "-w", HERE], check=True)

        print("vendored fasthttp/router %s: %d sources" % (ROUTER_VERSION, n_files))
    finally:
        if tmp:
            shutil.rmtree(tmp, ignore_errors=True)


if __name__ == "__main__":
    main()
