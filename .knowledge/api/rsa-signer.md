---
id: api:rsa-signer
type: api
title: RSA Signer Seam
---
`internal/rsasign` is the one seam behind which the RS256 signature is either `crypto/rsa` or a native backend. It is to decision:native-rsa-signing what api:tls-dialer is to the TLS backends, and it is much smaller.

```yaml
import_path: github.com/shibukawa/tinygodriver/internal/rsasign
state: proposed 2026-08-02
visibility: >
  internal. Unlike cloud/aws, there is no general-purpose surface here worth
  stabilizing: a caller who wants RSA on host go already has crypto/rsa.
go_surface: |
  type Key struct{ ... }   // holds a native handle or an *rsa.PrivateKey
  func ParsePKCS8(pem []byte) (*Key, error)
  func (k *Key) SignPKCS1v15SHA256(digest []byte) ([]byte, error)
  func (k *Key) Close() error
  func (k *Key) Bits() int
  const Backend string      // "crypto/rsa", "securetransport", "mbedtls", "cng"
lifecycle: >
  Close releases the native handle. A Key that is dropped without Close leaks
  it, the same obligation api:tls-dialer carries and for the same reason.
  api:google-auth holds one Key for the process, so this is one call, not a
  per-request pattern.
c_surface: |
  int  rsasign_load(const unsigned char *der, size_t len, void **out_key);
  int  rsasign_sign(void *key, const unsigned char *digest, size_t digest_len,
                    unsigned char *out, size_t out_cap, size_t *out_len);
  void rsasign_free(void *key);
  // 0 on success, stable negative codes otherwise; out-params written only on
  // success; the handle is a uintptr opaque to Go. See rule:cgo-bridge-contract.
input_format:
  go_side: PKCS#8 PEM, which is what a Google service-account file carries
  handed_to_c:
    darwin: PKCS#1 DER, unwrapped by the DER walker
    linux: the PEM unchanged; mbedtls_pk_parse_key reads PKCS#8
    windows: PKCS#1 DER, converted to a CNG blob by crypt32
  reason: >
    each backend takes the format it natively accepts. Normalizing on one would
    mean writing the conversion the OS already has.
der_walker:
  scope: >
    unwrap PrivateKeyInfo to its OCTET STRING. It reads a SEQUENCE, an INTEGER,
    a SEQUENCE and an OCTET STRING, and rejects everything else.
  not: a DER parser, an ASN.1 library, or anything that grows a second caller
  cost: 291ns, measured
  input_trust: an operator-supplied key file, not a remote input
build_selection:
  std_go: crypto/rsa, on every OS
  tinygo_darwin: securetransport-adjacent bridge into Security.framework
  tinygo_linux: system:mbedtls, already linked for TLS
  tinygo_windows: CNG through cgo
  unsupported: ErrPlatformNotSupported, never a silent fallback to a weaker path
  tags: rule:build-tag-selection
contract: rule:rsa-signer-agreement
satisfies: requirement:native-rsa-signing
decided_by: decision:native-rsa-signing
consumers: api:google-auth
counterpart: api:tls-dialer
