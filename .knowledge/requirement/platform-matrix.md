---
id: requirement:platform-matrix
type: requirement
title: Supported Platform Matrix
---
Support state per compiler and OS; unsupported combinations must fail at build time or with an explicit error, never with a silent plaintext fallback.

```yaml
priority: must
matrix:
  std_go_all_os:
    state: shipped
    backend: crypto/tls via requirement:std-go-delegation
    client_certs: yes
  tinygo_darwin_arm64:
    state: shipped
    backend: >
      hybrid by default, Network.framework for dial and Secure Transport for
      upgrade; system:mbedtls under -tags darwinstarttlswith13
    max_tls: 1.3 dial and 1.2 upgrade by default; 1.3 both with the tag
    in_band_upgrade: supported in both builds
    client_certs: no by default, yes with the tag
    see: decision:darwin-hybrid-tls
    verified: >
      tinygo binary completed HTTPS GET with custom-CA verification; untrusted
      root and hostname mismatch both rejected; 15 tests pass on the std and
      native paths
    client_certs: no, ErrClientCertificateUnsupported
    gate: requirement:macos-blocks-poc, passed
  tinygo_linux_arm64:
    state: shipped
    backend: system:mbedtls
    verified: >
      handshake with custom CA, untrusted root rejected, hostname mismatch
      rejected, skip-verify honored, AES and SHA known-answer vectors pass
    client_certs: yes, mbedTLS accepts PEM directly
    acceleration: measured, see rule:mbedtls-hw-acceleration
  tinygo_linux_amd64:
    state: >
      implemented; host-go runtime verified in an emulated container, native
      runtime still unverified. See linux_from_darwin in
      requirement:test-strategy.
    backend: system:mbedtls
    verified: same stage set as arm64, plus AES-NI detection and self tests
    throughput: unmeasured; the run was emulated, so a native CI job is needed
    client_certs: yes
  tinygo_wasip1_wasip2:
    state: >
      builds and fails explicitly, shipped 2026-08-07; no network or TLS
      backend exists
    backend: >
      none. netdev stubs report the missing socket backend, https returns
      ErrPlatformNotSupported, sqlite selects Backend none; see
      requirement:wasip1-netdev, requirement:wasip2-no-mbedtls and
      requirement:sqlite-conditional-link
    caveat: >
      wasip2 masquerades as GOOS=linux, so every linux constraint needs
      !wasip2; see rule:tinygo-wasip2-goos
    verified: >
      netdev+https+sqlite+pgxstdlib program builds for both targets and runs
      under wasmtime with explicit errors
  tinygo_windows_amd64:
    state: implemented; build verified, runtime unverified
    backend: system:schannel
    max_tls: 1.3 where the OS accepts SCH_CREDENTIALS, else 1.2
    in_band_upgrade: supported, on the same code path as dial
    client_certs: RSA only; EC returns ErrClientCertificateUnsupported
    verified: >
      cross-compiles, vets and links for windows/amd64 under mingw-w64, with
      and without -tags force_tinygo_logic. Never run on Windows.
    note: >
      the remaining gap and why wine is a poor stand-in are recorded in
      requirement:windows-tinygo-feasibility
    caveat: >
      tinygo cannot cross-compile to windows from darwin at all; `tinygo
      targets` lists no windows entry. The build check used host go with
      force_tinygo_logic, which is what requirement:test-strategy intends.
rsa_signing:
  note: >
    the first entry here that is not about TLS. Seam api:rsa-signer, decided by
    decision:native-rsa-signing.
  std_go_all_os:
    state: shipped by definition
    backend: crypto/rsa
  tinygo_darwin_arm64:
    state: shipped 2026-08-02
    backend: Security.framework, SecKeyCreateWithData plus SecKeyCreateSignature
    cost: 583 KB against 1040 KB for crypto/rsa, and 1.10ms against 2.87ms
  tinygo_linux:
    state: shipped 2026-08-03
    backend: system:mbedtls, already linked for TLS
    cost: >
      no new object code. Every needed module was already enabled in the
      vendored mbedtls_config.h for TLS.
    verified: >
      built and run in a golang:1.26 container, where it signs the committed
      vector to the same bytes crypto/rsa and Security.framework do
    structural_change: >
      the vendored mbedTLS moved from https/internal/mbedtls to
      internal/mbedtls. Go's internal rule made the old path unreachable from
      internal/rsasign, and compiling a second copy would have collided at link
      time, since C symbols are global.
  tinygo_windows_amd64:
    state: implemented 2026-08-03; cross-compiles and links, never executed
    backend: CNG, with crypt32 CryptDecodeObjectEx converting the key blob
    verified: >
      cross-compiles and vets for windows/amd64 under mingw-w64. Same state
      system:schannel ships in, and for the same reason.
    gate: requirement:windows-tinygo-feasibility
unsupported_behavior:
  returns: ErrPlatformNotSupported from api:tls-dialer, and from api:rsa-signer
  never: fall back to plaintext http or to an unverified connection
constraint: IPv4 only while system:tinygo-netdev is IPv4 only
asymmetry_to_document:
  - darwin has no client certificates; windows takes RSA only; linux and std go take any
  - linux ships a TLS implementation; darwin and windows use the OS one
  - linux binaries are about 3.8 MB against 272 KB on darwin
  - windows is the only platform where dial and upgrade share one backend
