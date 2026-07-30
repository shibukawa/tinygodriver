---
id: requirement:windows-tinygo-feasibility
type: requirement
title: Windows Backend, Implemented but Unrun
---
The Windows design is implemented and builds, but nothing here has been executed on a Windows machine.

```yaml
priority: should
state: implemented, runtime unverified
scope_note: >
  this file used to hold the provisional design. The design questions are now
  answered and recorded in system:schannel; what remains is the verification
  gap, which is what this file tracks.
settled_by_measurement:
  language:
    answer: c via cgo, not pure go
    superseded: >
      the original entry said pure go via syscall, on the assumption that
      syscall.NewLazyDLL was reachable. It is not.
    evidence: >
      tinygo 0.41.1 src/syscall contains no windows file. Build tags across
      that package cover linux, unix, wasip1, wasip2, js, nintendoswitch and
      baremetal only. There is no NewLazyDLL, no Syscall, no LoadLibrary.
      TinyGo's own crypto/rand/rand_windows.go reaches advapi32 through an
      //export declaration, not syscall.
    consequence: >
      rule:cgo-bridge-contract applies on windows after all. mingw-w64 was
      already required by system:tinygo-netdev, so no new dependency.
  model: buffer transform; Go owns the socket. Unchanged and confirmed.
  dial_and_upgrade:
    answer: one code path serves both
    reason: >
      SSPI never sees the socket, so a socket that already carried plaintext is
      indistinguishable from a fresh one. Windows needs no second backend for
      STARTTLS the way darwin does.
verified:
  cross_compile: >
    GOOS=windows GOARCH=amd64 CGO_ENABLED=1 CC=x86_64-w64-mingw32-gcc
    go build/vet, both with and without -tags force_tinygo_logic, on
    mingw-w64 14.0.0. The https and netdev test binaries link.
  host_unaffected: darwin build and the full https and netdev suites still pass
  wine_run:
    what: >
      wine 11.0 under rosetta 2, running the force_tinygo_logic https test
      binary and the netdev test binary
    result: https 10 of 21 pass; netdev sockets 6 of 6 pass
    passed:
      - TLS handshake and record I/O, via InsecureSkipVerify
      - the STARTTLS upgrade path, same code as dial
      - untrusted root rejected, on both dial and upgrade
      - RootCAsOnly rejects system anchors, and trusts nothing with no anchors
      - EC client key refused with ErrClientCertificateUnsupported
      - malformed PEM reported, client timeout honored
      - the whole netdev winsock layer
    also_confirmed: >
      CertGetCertificateChain and CertVerifyCertificateChainPolicy do run, and
      return specific statuses rather than a generic failure: a rejected chain
      reported CERT_E_UNTRUSTEDROOT 0x800B0109, which classifyStatus mapped to
      ErrUntrustedRoot end to end. Only the reject direction was reachable.
    never_executed_anywhere: >
      the paths behind the two wine blockers have run on no platform at all,
      which is the real residual risk and is larger than the failure count
      suggests:
        - a chain building cleanly, and the two-pass system-then-exclusive logic
          completing rather than failing
        - hostname matching in the success direction, via
          SSL_EXTRA_CERT_CHAIN_POLICY_PARA.pwszServerName
        - the CERT_TRUST revocation-bit mask in check_policy
        - CertCreateCertificateChainEngine actually yielding a usable engine
        - the client certificate path past the blob decode, including
          CertSetCertificateContextProperty and Schannel then using the key
    failed_for_wine_reasons: >
      all 11 failures trace to two wine stubs, not to this code. See
      wine_limits.
  wine_limits:
    chain_engine:
      symptom: E_INVALIDARG from CertCreateCertificateChainEngine
      cause: >
        wine's crypt32 accepts only the pre-Vista CERT_CHAIN_ENGINE_CONFIG.
        A probe confirmed cbSize 64 succeeds and cbSize 88, the size that
        carries hExclusiveRoot, fails. The struct layout here is correct:
        the probe measured sizeof at 88 for both the private declaration and
        mingw's own header.
      blocks: every test that needs a custom anchor to be accepted
      not_worked_around: >
        falling back to the 64-byte config would drop hExclusiveRoot and so
        silently restore the system roots, turning RootCAsOnly into a
        fail-open. Windows has no additive "system roots plus these" mode, so
        there is no correct fallback.
    root_store:
      finding: >
        wine's chain engine also ignores certificates added to the ROOT store
        at runtime, so there is no second route to a trusted custom anchor
        either.
      evidence: >
        a probe using only crypt32, with none of this repository's code, added
        a self-signed certificate through CertOpenSystemStoreW(0, "ROOT") and
        CertAddEncodedCertificateToStore. The call succeeded, the certificate
        read back out of the store, and CertGetCertificateChain still returned
        TrustStatus.dwErrorStatus 0x20, CERT_TRUST_IS_UNTRUSTED_ROOT.
      consequence: >
        chain verification in the ACCEPT direction is untestable under wine by
        any route. That is what 8 of the 11 failures were hiding.
    ncrypt:
      symptom: >
        "NCryptOpenStorageProvider: stub" and "NCryptImportKey Unhandled key
        magic 0x207"
      cause: wine's CNG is a stub; it cannot import a legacy RSA private blob
      evidence_the_code_is_right: >
        magic 0x207 is bVersion 2 with bType PRIVATEKEYBLOB, so
        CryptDecodeObjectEx had already produced a well-formed CAPI blob. Only
        the import failed.
    pkcs8:
      symptom: "CryptDecodeObjectEx Unimplemented decoder for OID 44"
      note: OID 44 is PKCS_PRIVATE_KEY_INFO, so the right decoder was called
    tls_parameters:
      symptom: "fixme:secur32:get_enabled_protocols handle TLS parameters"
      meaning: >
        wine accepted SCH_CREDENTIALS but ignores TLS_PARAMETERS, so min
        version was not enforced there. It also means the SCHANNEL_CRED
        fallback never ran and remains completely untested.
open_questions:
  custom_anchors:
    check: does hExclusiveRoot verification actually work on real Windows
    note: untestable under wine; this is the largest remaining gap
  tls13:
    check: whether a real Schannel negotiates 1.3 through SCH_CREDENTIALS
    note: wine accepted the structure but ignores the version constraint
  schannel_cred_fallback:
    check: the entire SCHANNEL_CRED path is unexercised
  client_certificates:
    check: whether the ephemeral CNG import actually satisfies Schannel
    scope: RSA only by design; see system:schannel
  recommended_route:
    what: a windows-latest CI job running the same suite
    why: >
      real Schannel, real certificate store, real CNG, real TLS 1.3. Wine has
      now given everything it can give; the remaining 11 tests need Windows.
how_to_run_on_windows:
  command: go test -tags force_tinygo_logic ./...
  trap: >
    a plain `go test ./...` does NOT exercise the https backend. Without the
    tag the package compiles roundtrip_std.go and delegates to crypto/tls, so
    it passes while saying nothing about Schannel. Verified with `go list`:
    without the tag the file set contains roundtrip_std.go and no
    dial_windows.go.
  netdev_exception: >
    netdev/tls_windows.go carries no tinygo tag, so netdev's TestIPProtoTLS
    hits Schannel even under a plain `go test ./netdev`.
  requires: >
    CGO_ENABLED=1 and a C compiler on PATH, because Schannel itself is cgo. Go
    on windows silently falls back to CGO_ENABLED=0 when it finds none; the
    backend then reports ErrPlatformNotSupported instead of failing to build.
  cgo_free_builds: >
    netdev builds and runs without cgo on windows since the pure-Go socket
    backend was added. Sockets work fully; only IPPROTO_TLS is unavailable.
    This is windows-only: darwin still fails to build netdev without cgo.
  link_check_done: >
    all 7 packages with tests cross-compile and link for windows/amd64 in both
    tag configurations, under mingw-w64 14.0.0.
sequencing: >
  the implementation is testable on host go windows with -tags
  force_tinygo_logic, independently of tinygo, which is what the CI job would
  use. tinygo itself cannot cross-compile to windows from darwin; `tinygo
  targets` lists none.
until_then:
  behavior: the backend is wired up and will be used; it is not gated off
  risk: >
    a defect surfaces as a failed handshake, not as a silent downgrade. No path
    falls back to plaintext or to an unverified connection.
```
