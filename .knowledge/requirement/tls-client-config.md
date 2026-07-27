---
id: requirement:tls-client-config
type: requirement
title: Custom CA, Client Certificate, and Skip-Verify
---
All backends must support additional trust anchors and an explicit verification bypass. Client certificates are supported everywhere except darwin, which refuses them rather than ignoring them.

```yaml
priority: must
features:
  custom_ca:
    input: PEM bytes or file path
    default_mode: append to system trust store
    exclusive_mode: RootCAsOnly ignores system anchors
    per_backend:
      darwin: SecTrustSetAnchorCertificates inside a verify block
      linux: mbedtls_ssl_conf_ca_chain, with the bundle from rule:linux-trust-store
      windows: temporary in-memory HCERTSTORE
      std_go: x509.CertPool
    status: supported everywhere
  client_certificate:
    input: cert PEM plus private key PEM
    key_types: [RSA, ECDSA P-256]
    per_backend:
      std_go:
        status: supported
        how: tls.X509KeyPair
      linux:
        status: supported
        how: mbedtls_ssl_conf_own_cert, which accepts PEM directly
      windows:
        status: provisional
        how: CERT_CONTEXT with CryptAcquireCertificatePrivateKey
      darwin:
        status: not supported
        returns: ErrClientCertificateUnsupported
        reason: >
          Network.framework needs a SecIdentityRef, obtainable only from a
          keychain. SecIdentityCreateWithCertificate needs the key resident,
          and SecPKCS12Import needs a PKCS#12 blob this package cannot build
          without crypto on the tinygo path.
        rule: refuse loudly; never ignore a configured certificate silently
        revisit: SecItemImport into a transient keychain
  insecure_skip_verify:
    effect: disables chain and hostname verification
    default: false
    status: supported everywhere
acceptance:
  - a self-signed CA server is reachable with WithRootCAFile and rejected without it
  - InsecureSkipVerify=true connects to a self-signed host; false returns a
    distinguishable certificate error per requirement:error-classification
  - an mTLS server accepts the client cert on std go and linux
  - darwin returns ErrClientCertificateUnsupported rather than connecting
  - the identical test table runs on every backend, with the darwin client-cert
    case asserting the refusal
configured_by: data:https-config
defaults: rule:certificate-verification-default
