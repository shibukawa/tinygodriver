---
id: decision:native-rsa-signing
type: decision
title: RS256 Signing Goes Native Under TinyGo
---
Under TinyGo the RS256 signature is computed by the OS or the vendored crypto library, not by `crypto/rsa`. Host Go keeps `crypto/rsa`, the same std-versus-native split rule:build-tag-selection already applies to TLS.

```yaml
state: proposed
proposed_on: 2026-08-02
measured_with: tinygo 0.41.1 darwin/arm64, go 1.26.5, Security.framework
not_a_speed_decision: >
  requirement:google-auth-validation measured 2.9ms per signature in pure Go
  under tinygo, once an hour. That is not a problem worth a native backend for.
  The reason is binary size, and the speed is a side effect.
measured_darwin:
  spike: a hand-declared SecKeyCreateWithData plus SecKeyCreateSignature bridge
  size:
    isolated_fixture:
      pure_go: 1040 KB
      native: 583 KB
      baseline: 452 KB for the same program with neither
      reading: crypto/rsa plus crypto/x509 cost 588 KB; the bridge costs 131 KB
    real_client:
      what: examples/datastoredemo, measured 2026-08-02 once it existed
      pure_go: 1920480 bytes
      native: 1563392 bytes
      saved: 357 KB, 19% of the binary
      why_smaller: >
        https links crypto/x509 for certificate error types regardless, so only
        the RSA math comes out. The fixture figure was right about the fixture
        and wrong as a general claim.
      still_worth_it: >
        yes. 357 KB is more than the entire darwin https client, which
        requirement:platform-matrix records at 272 KB.
  speed:
    tinygo_native: 1.10-1.17ms
    tinygo_pure_go: 2.87ms
    host_go_pure_go: 1.05-1.16ms
    reading: >
      identical to host go, because the work happens in Apple's assembly either
      way. The purego bigmod penalty in requirement:google-auth-validation
      disappears entirely rather than shrinking.
  key_load: 784us under tinygo, once per process
the_risk_that_had_to_be_cleared:
  what: >
    SecKeyCreateWithData accepts a PKCS#1 RSAPrivateKey. Google ships PKCS#8.
    If unwrapping needed crypto/x509 the whole size argument collapsed, because
    x509 is the larger half of the 588 KB.
  result: >
    cleared. PrivateKeyInfo is SEQUENCE { INTEGER, SEQUENCE, OCTET STRING }, so
    the RSAPrivateKey comes out with about 40 lines of hand-rolled DER walking
    and no encoding/asn1. Measured at 291ns, 1217 bytes in and 1191 out.
  caution: >
    that walker accepts only what it needs and rejects everything else. It is
    not a DER parser and must never grow into one.
per_platform:
  darwin:
    backend: Security.framework, SecKeyCreateWithData and SecKeyCreateSignature
    state: measured, works under tinygo with hand-declared symbols
    input: PKCS#1, unwrapped in Go
    frameworks: Security and CoreFoundation, both already linked by internal/securetransport
  linux:
    backend: system:mbedtls, mbedtls_pk_parse_key and mbedtls_pk_sign
    state: designed, not measured
    input: >
      PKCS#8 PEM directly. mbedTLS parses it, so linux needs no DER walker at all.
    cost: >
      zero. MBEDTLS_RSA_C, PKCS1_V15, PK_C, PK_PARSE_C, ASN1_PARSE_C,
      PEM_PARSE_C, BASE64_C, MD_C and SHA256_C are all already enabled in the
      vendored mbedtls_config.h for TLS, so no config change and no new object
      code. Checked against the vendored header, not assumed.
    reading: >
      linux is the cheapest of the three and the only one where the native path
      is strictly less work than the Go path
  windows:
    backend: CNG, BCryptImportKeyPair and BCryptSignHash
    state: designed, not measured, and not measurable here
    input_problem: >
      BCRYPT_RSAPRIVATE_BLOB wants the key components as big-endian byte arrays
      behind a header, so a DER unwrap is not enough; the PKCS#1 structure has
      to be taken apart.
    intended_path: >
      CryptDecodeObjectEx with CNG_RSA_PRIVATE_KEY_BLOB converts PKCS#1 DER to
      the blob directly, and crypt32 is already linked for schannel certificate
      handling. This keeps the parsing in the OS rather than in the walker.
    gate: requirement:windows-tinygo-feasibility, unchanged
std_go: >
  crypto/rsa. There is no reason to cross a cgo boundary on a build where the
  Go implementation is assembly-backed and free.
why_this_is_the_easiest_native_backend_here:
  determinism: >
    RSASSA-PKCS1-v1_5 is deterministic, so the native and Go paths must produce
    byte-identical signatures. Every other native backend in this repository is
    a handshake, which can only be tested against a live peer.
  contract: rule:rsa-signer-agreement
  no_socket: >
    bytes in, bytes out, plus a key handle. decision:per-os-integration-model
    classifies backends by who owns the socket, and this one owns none, so it
    sits beside that taxonomy rather than inside it.
  surface: four C entry points, against the connection lifecycle in api:tls-dialer
rejected:
  pure_go_everywhere:
    reason: 588 KB on a target where the whole https client is 272 KB on darwin
    kept_as: the host go path and the reference implementation for the known-answer tests
  external_token_only:
    what: require callers to supply a token and never sign anything
    reason: >
      it was the size escape hatch in decision:google-token-strategy, and native
      signing removes most of what it was escaping. It stays as an option for
      provisioned devices, not as the size answer.
  native_sha256:
    reason: 2us against a 1100us signature; crossing cgo for it would cost more than it saves
  native_pkcs8_parse_on_darwin:
    reason: >
      SecItemImport can read PKCS#8, but it wants a keychain interaction and an
      external-format description. 40 lines of DER is smaller and has no
      ambient state.
consequences:
  - >
    decision:google-token-strategy's linkage argument weakens: not signing now
    saves ~131 KB rather than ~590 KB, so StaticTokenSource is a deployment
    choice rather than a size choice
  - a fourth native surface to build and test on three platforms, though a small one
  - >
    requirement:platform-matrix gains a row that is not about TLS, which is the
    first time that concept has covered anything else
  - >
    the DER walker is security-relevant code this repository now owns. It reads
    a key the operator supplied, not a remote input, which is what makes 40
    lines acceptable.
delivers: requirement:native-rsa-signing
seam: api:rsa-signer
evidence: requirement:google-auth-validation
constrained_by: rule:tinygo-darwin-toolchain, rule:tinygo-cgo-flag-limits, rule:cgo-bridge-contract
used_by: api:google-auth
