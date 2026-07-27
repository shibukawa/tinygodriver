---
id: requirement:no-crypto-tls-on-tinygo
type: requirement
title: No crypto/tls or crypto/x509 on the TinyGo Path
---
Files compiled under TinyGo must not import `crypto/tls` or `crypto/x509`, directly or transitively.

```yaml
priority: must
reason:
  - tinygo crypto/tls is a stub; linking it yields runtime failure not a build error
  - crypto/x509 pulls a large parser and trust logic that native backends already own
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
