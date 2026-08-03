---
id: requirement:native-rsa-signing
type: requirement
title: RSA Signing Must Not Link crypto/rsa Under TinyGo
---
A TinyGo build that signs a JWT must reach the RSA operation through the platform backend behind api:rsa-signer, and must contain no `crypto/rsa`, `crypto/x509` or `bigmod` code. Host Go builds keep `crypto/rsa`.

```yaml
priority: must
state:
  darwin: spiked and measured 2026-08-02; not yet integrated
  linux: not started; the backend is already linked for TLS
  windows: not started; blocked on requirement:windows-tinygo-feasibility
  std_go: satisfied by construction
budget:
  what_this_requirement_defends: binary size, not latency
  isolated_fixture:
    crypto_rsa_and_x509: 588 KB over a 452 KB baseline
    native_bridge: 131 KB over the same baseline
    caveat: >
      this is a program that links no TLS, so it overstates the saving for a
      real client; see in_a_real_client
  in_a_real_client:
    measured_on: 2026-08-02, examples/datastoredemo built with tinygo 0.41.1
    pure_go: 1920480 bytes
    native: 1563392 bytes
    saved: 357 KB, 19% of the binary
    why_less_than_the_fixture: >
      https already links crypto/x509 for its certificate error types, about 271
      symbols, whether or not anything signs. Only the RSA math is actually
      removed. Corrected after building the real client; the 457 KB first
      recorded here was a fixture number generalized too far.
  latency_is_not_the_reason: >
    2.87ms to 1.10ms is real and irrelevant at one signature per hour. Stated so
    a future reader does not defend the wrong property.
  measurement: requirement:google-auth-validation
acceptance:
  absence:
    check: "nm on the tinygo binary reports zero crypto/rsa and zero bigmod symbols"
    scope: >
      those two are the RSA math and the property holds everywhere. crypto/x509
      is deliberately not in the list: https pulls it for TLS error reporting
      independently of signing, so requiring zero would be a criterion no client
      can meet and would have to be waived on first contact.
    verified_runnable: >
      2026-08-02. The isolated fixture: pure-Go 59 crypto/rsa and 73 bigmod
      symbols, native 0 and 0, with 3 SecKey present. examples/datastoredemo:
      the same 0 and 0, against 59 and 73 when the pure-Go path is forced.
    why_symbols_not_size: >
      a size threshold drifts with every unrelated change and passes for the
      wrong reason. Symbol absence states the actual property.
  agreement:
    - every backend signs the committed vector to identical bytes, per rule:rsa-signer-agreement
    - a native signature verifies under crypto/rsa VerifyPKCS1v15 on host go
  parsing:
    - the DER walker turns the committed PKCS#8 key into the expected PKCS#1 bytes
    - truncated, over-long and wrongly tagged inputs are rejected, never misread
    - the walker gains no second caller and no second input shape
  lifecycle:
    - Close releases the native handle, and a double Close is safe
    - signing after Close errors rather than crashing
    - no key handle outlives the process; api:google-auth holds exactly one
  platform:
    - an unsupported combination returns ErrPlatformNotSupported at the seam
    - never a silent fallback to crypto/rsa, which would pass the absence check on one build and fail it on another
  build:
    - the package builds under tinygo and under go test -tags force_tinygo_logic
    - a 4096-bit key round-trips, exercising the 512-byte output buffer
constraints:
  bridge: rule:cgo-bridge-contract, unchanged; four C entry points, no callbacks into Go
  toolchain: rule:tinygo-darwin-toolchain and rule:tinygo-cgo-flag-limits; no framework headers
  no_new_link_dependency:
    darwin: Security and CoreFoundation, both already linked by internal/securetransport
    linux: >
      the vendored mbedTLS as configured today. Enabling a new MBEDTLS_* module
      would breach this constraint and is not needed; checked 2026-08-02.
    windows: crypt32 and bcrypt, of which crypt32 is already linked for schannel
  no_background_goroutine: >
    rule:tinygo-threads-scheduler applies here as it does to the pool in
    requirement:connection-reuse, though signing is synchronous and does not
    tempt one
non_goals:
  verification: >
    nothing in this repository verifies an RSA signature at runtime. The
    known-answer tests do, on host go, which is where crypto/rsa is free.
  es256: not implemented on any backend; requirement:google-auth-validation did not measure it
  keychain_integration: >
    no SecItemImport, no keychain-resident identity. The key comes from a file
    the operator supplied, which is also what keeps the DER walker's input
    trusted.
  general_crypto_package: >
    api:rsa-signer stays internal. A caller wanting RSA on host go has
    crypto/rsa, and this seam exists only to serve api:google-auth.
  hashing: >
    the digest stays in Go. 2us against a 1100us signature, so crossing cgo for
    it would cost more than it saves.
risks:
  windows_unverifiable: >
    the CNG path cannot be run on this machine, so it ships in the same state as
    system:schannel: cross-compiled, linked, never executed
  security_relevant_code: >
    the DER walker reads a private key. It is 40 lines and its input is
    operator-supplied rather than remote, which is what makes hand-rolling it
    acceptable rather than reckless. If either fact changes, this requirement
    should be revisited.
  fourth_native_surface: >
    three more backends to carry. Smaller than the TLS ones, and unlike them
    fully testable offline, but it is still per-OS code that must be maintained
    when a toolchain moves.
decided_by: decision:native-rsa-signing
surface: api:rsa-signer
contract: rule:rsa-signer-agreement
evidence: requirement:google-auth-validation
consumer: api:google-auth
