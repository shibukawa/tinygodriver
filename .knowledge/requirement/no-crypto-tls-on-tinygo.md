---
id: requirement:no-crypto-tls-on-tinygo
type: requirement
title: No crypto/tls or crypto/x509 on the TinyGo Path
---
Files compiled under TinyGo must not import `crypto/tls` or `crypto/x509`, directly or transitively.

```yaml
priority: must
reason:
  - >
    tinygo crypto/tls is a stub, and since 0.42 a silent one. tls.Client and
    tls.Server are both `return &Conn{Conn: conn}` -- no handshake, no
    encryption -- and tls.NewListener wraps with Server, so it is a plaintext
    listener claiming to be TLS. Handshake() returns nil. Measured 2026-09-02:
    against a plaintext HTTP server, host go fails with "first record does not
    look like a TLS handshake" while tinygo 0.42 returns nil and puts an
    Authorization header on the wire in the clear
  - >
    0.41 panicked here, which at least failed loudly. The upgrade turned a
    crash into silent cleartext, so this requirement got stronger, not weaker
  - crypto/x509 pulls a large parser and trust logic that native backends already own
still_absent_at_0_42: [tls.X509KeyPair, http.Server.ServeTLS]
real_tls_path: >
  tls.Dial and tls.DialWithDialer are not stubs: they route to net.DialTLS,
  i.e. netdev IPPROTO_TLS. Dialing is fine; wrapping an existing net.Conn is
  not
consequences:
  - data:https-config defines its own Config, Version, and KeyPair types
  - PEM parsing does not happen in Go; PEM bytes pass through to the backend
  - certificate errors are reconstructed from native codes, not from x509 types
acceptance:
  - "`tinygo build` of the example succeeds on darwin"
  - a source check rejects these imports in any file with a tinygo build tag
  - test helpers that need crypto/tls live in files tagged "!tinygo"
tension: >
  requirement:test-strategy needs crypto/tls to run a local test server, so test
  servers stay on the host-go side of the build tags
