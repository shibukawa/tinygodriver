#!/usr/bin/env python3
"""Vendor mbedTLS into this directory.

Usage:
    python3 vendor.py /path/to/mbedtls-3.6.7.tar.bz2

Re-run this after bumping MBEDTLS_VERSION to pick up a security release. Every
transformation below is also recorded in PATCHES.md; keep the two in sync.

Why the sources land flat in this directory rather than a subdirectory: cgo
only compiles C files that sit in the Go package directory, and it puts that
directory on the include path automatically. Relying on that is what lets the
build work without -I, which matters because TinyGo does not expand ${SRCDIR}
in #cgo lines and ignores CGO_CFLAGS.
"""

import os
import re
import shutil
import subprocess
import sys
import tarfile
import tempfile

MBEDTLS_VERSION = "3.6.7"
EXPECTED_SHA256 = "a7e8bcbec0e6f761b4af24f25677626b35f762f68eef79c08677a363212d11f6"

HERE = os.path.dirname(os.path.abspath(__file__))

BUILD_TAG = "//go:build (tinygo || force_tinygo_logic) && linux\n\n"

# Sources that cannot or should not be built here.
DROP_SOURCES = {
    # Needs the BSD socket API, which TinyGo's musl does not provide.
    # tls_mbedtls.c supplies BIO callbacks instead.
    "net_sockets.c",
    # Unused, and pulls in the timing API.
    "timing.c",
}

# Config options switched off: each needs a platform facility we do not have,
# or pulls in code we do not use.
CONFIG_DISABLE = [
    "MBEDTLS_NET_C",
    "MBEDTLS_TIMING_C",
    "MBEDTLS_FS_IO",
    "MBEDTLS_PSA_ITS_FILE_C",
    "MBEDTLS_PSA_CRYPTO_STORAGE_C",
]

# Config options switched on: hardware acceleration, the known-answer self
# tests that validate it, and certificate validity-period checking.
#
# MBEDTLS_HAVE_TIME_DATE must stay on. Without it mbedTLS skips notBefore and
# notAfter entirely and happily accepts an expired certificate; the package
# test suite covers exactly this.
CONFIG_ENABLE = [
    "MBEDTLS_HAVE_TIME_DATE",
    "MBEDTLS_SELF_TEST",
    "MBEDTLS_SHA256_USE_ARMV8_A_CRYPTO_IF_PRESENT",
    "MBEDTLS_SHA512_USE_A64_CRYPTO_IF_PRESENT",
]


def fail(msg):
    print("vendor.py: " + msg, file=sys.stderr)
    sys.exit(1)


def check_tarball(path):
    out = subprocess.run(
        ["shasum", "-a", "256", path], capture_output=True, text=True
    ).stdout.split()
    if not out:
        out = subprocess.run(
            ["sha256sum", path], capture_output=True, text=True
        ).stdout.split()
    if not out or out[0] != EXPECTED_SHA256:
        fail("sha256 mismatch for %s\n  got      %s\n  expected %s"
             % (path, out[0] if out else "?", EXPECTED_SHA256))
    print("sha256 ok")


def clean():
    """Remove a previous vendoring, leaving hand-written files alone."""
    keep = {"vendor.py", "PATCHES.md", "tinygo_arm_neon.h", "tls_mbedtls.c",
            "tls_mbedtls.h", "mbedtls.go", "doc.go", "unsupported.go", "errors.go",
            "cgoflags_linux_tinygo.go", "cgoflags_linux_hostgo.go",
            "mbedtls_test.go"}
    for name in os.listdir(HERE):
        if name in keep:
            continue
        path = os.path.join(HERE, name)
        if os.path.isdir(path):
            shutil.rmtree(path)
        else:
            os.remove(path)


def main():
    if len(sys.argv) != 2:
        fail("usage: vendor.py /path/to/mbedtls-%s.tar.bz2" % MBEDTLS_VERSION)
    tarball = sys.argv[1]
    check_tarball(tarball)

    with tempfile.TemporaryDirectory() as tmp:
        with tarfile.open(tarball) as tf:
            tf.extractall(tmp)
        roots = [d for d in os.listdir(tmp) if os.path.isdir(os.path.join(tmp, d))]
        if len(roots) != 1:
            fail("unexpected tarball layout: %r" % roots)
        src = os.path.join(tmp, roots[0])

        clean()

        lib = os.path.join(src, "library")
        n_src = 0
        for name in sorted(os.listdir(lib)):
            if name in DROP_SOURCES:
                continue
            if name.endswith(".c"):
                # A build constraint is required: without one, a std-Go build
                # would try to compile these with cgo disabled and fail.
                with open(os.path.join(lib, name)) as f:
                    body = f.read()
                with open(os.path.join(HERE, name), "w") as f:
                    f.write(BUILD_TAG + body)
                n_src += 1
            elif name.endswith(".h"):
                shutil.copy(os.path.join(lib, name), os.path.join(HERE, name))

        # Public headers go in subdirectories of the package directory so that
        # #include "mbedtls/ssl.h" resolves with no -I flag.
        for sub in ("mbedtls", "psa"):
            s = os.path.join(src, "include", sub)
            if os.path.isdir(s):
                shutil.copytree(s, os.path.join(HERE, sub))

        shutil.copy(os.path.join(src, "LICENSE"), os.path.join(HERE, "LICENSE"))

        patch_config(os.path.join(HERE, "mbedtls", "mbedtls_config.h"))
        patch_common(os.path.join(HERE, "common.h"))

    print("vendored mbedTLS %s: %d sources" % (MBEDTLS_VERSION, n_src))


def patch_config(path):
    with open(path) as f:
        text = f.read()
    for opt in CONFIG_DISABLE:
        text, n = re.subn(r"(?m)^#define %s$" % opt, "//#define %s" % opt, text)
        if n == 0:
            fail("could not disable %s; upstream config changed" % opt)
    for opt in CONFIG_ENABLE:
        if re.search(r"(?m)^#define %s$" % opt, text):
            continue  # already on upstream by default
        text, n = re.subn(r"(?m)^//#define %s$" % opt, "#define %s" % opt, text)
        if n == 0:
            fail("could not enable %s; upstream config changed" % opt)
    with open(path, "w") as f:
        f.write(text)
    print("patched mbedtls_config.h")


def patch_common(path):
    """Route the NEON include through our header on TinyGo builds.

    common.h keys NEON off __ARM_NEON, a compiler predefine no mbedTLS option
    controls and that TinyGo rejects -U for. TinyGo also has no arm_neon.h at
    all, so it gets tinygo_arm_neon.h instead. Host Go has a complete
    toolchain and keeps the real header.
    """
    with open(path) as f:
        text = f.read()
    old = "#if defined(__ARM_NEON)\n#include <arm_neon.h>\n"
    new = ("#if defined(__ARM_NEON) && !defined(MBEDTLS_NO_NEON_INTRINSICS)\n"
           "#if defined(MBEDTLS_TINYGO_NEON)\n"
           "#include \"tinygo_arm_neon.h\"\n"
           "#else\n"
           "#include <arm_neon.h>\n"
           "#endif\n")
    if old not in text:
        fail("common.h NEON include not found; upstream changed")
    text = text.replace(old, new, 1)
    with open(path, "w") as f:
        f.write(text)
    print("patched common.h")


if __name__ == "__main__":
    main()
