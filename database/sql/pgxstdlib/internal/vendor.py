#!/usr/bin/env python3
"""Vendor pgx into internal/pgx for the TinyGo build.

Usage:
    python3 vendor.py                       # use the module cache
    python3 vendor.py /path/to/pgx-src      # use an unpacked source tree

Re-run this after bumping PGX_VERSION. Every transformation below is also
recorded in PATCHES.md; keep the two in sync.

Why the whole module is copied rather than only the three patched files:
pgconn imports github.com/jackc/pgx/v5/internal/{pgio,iobufpool} and
pgconn/internal/bgreader, and Go refuses to let another module import those.
A partial copy therefore cannot compile. Rewriting every import path to live
under this repository makes pgx's internal packages our internal packages,
which is legal, and needs no replace directive -- a replace would be ignored
in a dependency, so consumers of this library would silently get upstream pgx
and fail to build under TinyGo.
"""

import os
import re
import shutil
import subprocess
import sys
import zipfile

PGX_VERSION = "v5.10.0"
PGX_MODULE = "github.com/jackc/pgx/v5"

HERE = os.path.dirname(os.path.abspath(__file__))
DEST = os.path.join(HERE, "pgx")
LOCAL_MODULE = "github.com/shibukawa/tinygodriver/database/sql/pgxstdlib/internal/pgx"

# Only what pgx/stdlib actually needs. Measured with `go list -deps`; stdlib
# sits on top of the whole stack, so this is nearly the entire module.
KEEP_DIRS = {
    "",
    "pgconn",
    "pgconn/ctxwatch",
    "pgconn/internal/bgreader",
    "pgproto3",
    "pgtype",
    "pgxpool",
    "stdlib",
    "internal/iobufpool",
    "internal/pgio",
    "internal/sanitize",
    "internal/stmtcache",
}

# Test-only helpers that would drag in extra dependencies.
DROP_DIRS = {"internal/pgmock", "internal/faultyconn"}


def fail(msg):
    print("vendor.py: " + msg, file=sys.stderr)
    sys.exit(1)


def source_tree():
    """Return a directory holding the pgx sources, extracting from the module
    cache when the caller did not supply one."""
    if len(sys.argv) > 1:
        path = os.path.abspath(sys.argv[1])
        if not os.path.isdir(path):
            fail("not a directory: " + path)
        return path, None

    gomodcache = subprocess.run(
        ["go", "env", "GOMODCACHE"], capture_output=True, text=True, check=True
    ).stdout.strip()
    zip_path = os.path.join(
        gomodcache, "cache", "download", PGX_MODULE, "@v", PGX_VERSION + ".zip"
    )
    if not os.path.exists(zip_path):
        fail(
            "module zip not found: %s\nrun: go mod download %s@%s"
            % (zip_path, PGX_MODULE, PGX_VERSION)
        )

    tmp = os.path.join(HERE, ".pgx-src")
    shutil.rmtree(tmp, ignore_errors=True)
    with zipfile.ZipFile(zip_path) as z:
        z.extractall(tmp)
    root = os.path.join(tmp, PGX_MODULE + "@" + PGX_VERSION)
    if not os.path.isdir(root):
        fail("unexpected zip layout under " + tmp)
    return root, tmp


def wanted(rel_dir):
    rel_dir = rel_dir.replace(os.sep, "/")
    if rel_dir == ".":
        rel_dir = ""
    if rel_dir in DROP_DIRS:
        return False
    return rel_dir in KEEP_DIRS


def rewrite(text):
    """Point every pgx import at this repository."""
    return text.replace(PGX_MODULE, LOCAL_MODULE)


def main():
    src, tmp = source_tree()
    try:
        shutil.rmtree(DEST, ignore_errors=True)
        os.makedirs(DEST)

        n_files = 0
        for dirpath, dirnames, filenames in os.walk(src):
            rel_dir = os.path.relpath(dirpath, src)
            if not wanted(rel_dir):
                continue
            for name in sorted(filenames):
                if not name.endswith(".go") or name.endswith("_test.go"):
                    continue
                out_dir = DEST if rel_dir == "." else os.path.join(DEST, rel_dir)
                os.makedirs(out_dir, exist_ok=True)
                with open(os.path.join(dirpath, name), encoding="utf-8") as f:
                    body = f.read()
                with open(os.path.join(out_dir, name), "w", encoding="utf-8") as f:
                    f.write(rewrite(body))
                n_files += 1

        # MIT requires the notice to travel with the source.
        shutil.copy(os.path.join(src, "LICENSE"), os.path.join(DEST, "LICENSE"))

        stamp = os.path.join(DEST, "VERSION")
        with open(stamp, "w", encoding="utf-8") as f:
            f.write(
                "%s %s\nvendored by vendor.py; see PATCHES.md for local changes\n"
                % (PGX_MODULE, PGX_VERSION)
            )

        # Rewriting the import paths reorders import groups, so the result is
        # no longer gofmt-clean. A vendored tree that fails gofmt -l is noise in
        # every future diff.
        subprocess.run(["gofmt", "-w", DEST], check=True)

        print("vendored pgx %s: %d sources" % (PGX_VERSION, n_files))
        print("now apply the patches in PATCHES.md")
    finally:
        if tmp:
            shutil.rmtree(tmp, ignore_errors=True)


if __name__ == "__main__":
    main()
