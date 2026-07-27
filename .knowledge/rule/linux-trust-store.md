---
id: rule:linux-trust-store
type: rule
title: Linux System Trust Store
---
mbedTLS has no system trust store, so the Go side loads the distribution CA bundle and passes PEM to the backend.

```yaml
problem: >
  system:network-framework and system:schannel evaluate trust against the OS
  keychain or certificate store. system:mbedtls has neither, so a default
  https.Get would trust nothing unless the anchors are supplied.
solution:
  where: Go, not C
  action: read the first CA bundle that exists and pass its PEM to the backend
  fits: data:https-config already carries PEM anchors
search_order:
  - $SSL_CERT_FILE
  - $SSL_CERT_DIR
  - /etc/ssl/certs/ca-certificates.crt
  - /etc/pki/tls/certs/ca-bundle.crt
  - /etc/ssl/ca-bundle.pem
  - /etc/ssl/cert.pem
rationale: >
  the first two match OpenSSL convention, so existing deployment tooling keeps
  working. The rest cover Debian, RHEL, SUSE, and Alpine layouts.
rules:
  - load once per process and cache; the bundle is large
  - RootCAs from data:https-config are appended to the system bundle
  - RootCAsOnly skips the system bundle entirely
  - a missing bundle is an error at dial time, never a silent skip of verification
  - the error must name the paths searched, because this is a deployment problem
applies_to: system:mbedtls
not_applicable: darwin and windows, which use the OS store
