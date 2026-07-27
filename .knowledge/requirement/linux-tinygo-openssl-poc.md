---
id: requirement:linux-tinygo-openssl-poc
type: requirement
title: Spike — TinyGo/Linux OpenSSL Re-verification
---
Closed. Re-verification showed the failure is real but the recorded explanation was wrong, and that two independent blockers make OpenSSL unusable under TinyGo/Linux.

```yaml
priority: must
state: closed
outcome: >
  superseded by decision:linux-mbedtls. Both OpenSSL routes are dead ends, and
  the findings below are kept as the evidence for that decision and as material
  for a possible tinygo upstream report.
verified_on: 2026-07-26
environment:
  container: debian 13 aarch64, tinygo 0.41.1, go 1.26.2, openssl 3.5.6
  control: host go with cgo in the same image passes every stage
prior_claim:
  text: tinygo linux bypasses glibc process initialization
  verdict: refuted as stated; the real first cause is a missing PT_INTERP
blocker_1_relocations:
  state: solved
  symptom: SIGSEGV with PC = 0x0 on the first call into libcrypto
  evidence:
    - gdb backtrace shows the call target is address 0
    - readelf shows DT_NEEDED, .gnu.version_r, and a JUMP_SLOT relocation present
    - readelf shows no PT_INTERP segment
    - LD_DEBUG produces no output at all, so ld.so never runs
  cause: >
    tinygo emits a dynamically-referencing executable with no interpreter, so
    nothing ever applies the relocations and the GOT entry stays null
  fix: 'tinygo build -ldflags="-extldflags=-dynamic-linker=/lib/ld-linux-aarch64.so.1"'
  result: PT_INTERP present, shared libssl loads, OpenSSL_version_num returns 0x30500060
  caveat: >
    -dynamic-linker is rejected inside #cgo LDFLAGS, so the flag cannot be
    embedded in the package; users must pass -ldflags themselves. The path is
    also architecture specific.
blocker_2_ctx_init:
  state: open
  symptom: SSL_CTX_new returns NULL even after blocker_1 is fixed
  evidence:
    - OPENSSL_init_crypto and OPENSSL_init_ssl both return 1
    - RAND_status is 1, OSSL_PROVIDER_load("default") succeeds
    - failure raised at ssl/ssl_lib.c:4074 in SSL_CTX_new_ex
    - error 0x0a080014, lib "SSL routines", reason string unresolved
    - identical code under host go in the same image succeeds
  scheduler_tested:
    threads: reaches the SSL_CTX_new failure; the default and the best case
    tasks: segfaults earlier
    none: segfaults earlier
    conclusion: the scheduler is not the cause; do not pursue this further
  next_step: >
    ssl_lib.c:4074 is near the default cipher list construction. Bisect
    SSL_CTX_new_ex against a debug OpenSSL to name the failing call. Until
    then tinygo linux stays unsupported.
static_link_alternative:
  state: rejected for shipping
  finding: mechanically achievable but fragile
  detail: >
    static libssl.a/libcrypto.a link only after roughly 40 shim symbols added
    over 7 rounds: zstd and zlib archives, __memcpy_chk, __memset_chk,
    __fprintf_chk, __vfprintf_chk, poll, stat, socketpair, setsockopt,
    getsockopt, getsockname, getpeername, recvmmsg, sendmmsg, recvfrom,
    sendto, recvmsg, tcgetattr, tcsetattr, dlopen family, ucontext family,
    aarch64 outline atomics, and __floatscan, which is a hidden symbol missing
    from tinygo's own musl archive
  why_rejected: >
    the symbol set is dictated by how the distribution compiled OpenSSL, so it
    would differ on every distro and release
related_finding:
  - tinygo linux ships musl, and its libc omits the BSD socket API entirely,
    which is why netdev/sys_linux.go already issues raw syscalls
