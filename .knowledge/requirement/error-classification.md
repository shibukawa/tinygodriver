---
id: requirement:error-classification
type: requirement
title: Distinguishable TLS Errors
---
Native status codes must map to sentinel errors an application can branch on with `errors.Is`, uniformly across backends.

```yaml
priority: should
sentinels:
  - ErrHandshakeFailed
  - ErrCertificateInvalid
  - ErrCertificateExpired
  - ErrHostnameMismatch
  - ErrUntrustedRoot
  - ErrClientCertificateRejected
  - ErrProtocolVersion
  - ErrPlatformNotSupported
  - ErrClientCertificateUnsupported
error_type: |
  type Error struct {
      Op      string // "dial", "handshake", "read", "write"
      Host    string
      Backend string // "network", "mbedtls", "schannel", "crypto/tls"
      Code    int    // native status code, for diagnosis
      Err     error  // one of the sentinels
  }
mapping_sources:
  darwin:
    from: nw_error_get_error_code, which yields Secure Transport OSStatus
    examples:
      -9808: ErrCertificateInvalid
      -9807: ErrUntrustedRoot
      -9814: ErrCertificateExpired
      -9843: ErrHostnameMismatch
    limitation: >
      with a custom CA the verify block returns a single accept/reject, so a
      hostname mismatch and an untrusted chain can both surface as
      ErrCertificateInvalid. Without a custom CA the framework's own codes are
      more specific. Document this rather than pretending otherwise.
  linux:
    from: mbedTLS negative error codes, plus the verify bitmask
    coarse: MBEDTLS_ERR_X509_CERT_VERIFY_FAILED -0x2700 for any verify failure
    refinement: >
      mbedtls_ssl_get_verify_result returns a flag bitmask, so BADCERT_EXPIRED,
      BADCERT_CN_MISMATCH and BADCERT_NOT_TRUSTED map to distinct sentinels.
      Use it; the bare return code alone is not specific enough.
  windows: SEC_E_* status from InitializeSecurityContext
  std_go: crypto/tls and crypto/x509 error types
acceptance:
  - each sentinel is produced by at least one test per backend that can raise it
  - Error message never contains a raw native pointer or handle
  - errors returned from RoundTrip survive http.Client unwrapped
current_gap: >
  netdev/tls_errors.go collapses everything into four generic strings; that
  granularity is insufficient here
