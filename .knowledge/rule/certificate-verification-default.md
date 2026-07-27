---
id: rule:certificate-verification-default
type: rule
title: Verification Is On By Default
---
The zero-value config verifies the peer chain and hostname against the system trust store; weakening it requires an explicit, named opt-in.

```yaml
rules:
  - zero-value Config performs full verification
  - InsecureSkipVerify has no environment-variable or build-tag equivalent
  - a verification failure never degrades to a warning or a plaintext retry
  - custom CAs are added to system anchors, not substituted, unless RootCAsOnly
  - minimum protocol version is TLS 1.2
  - an unsupported platform returns an error rather than an unverified connection
documentation_duty:
  - the README must state which trust store each backend uses
  - InsecureSkipVerify doc comment must say it is for testing only
rationale: >
  the three backends fail in different ways, so a permissive default would be
  inconsistently unsafe across platforms
applies_to:
  - data:https-config
  - requirement:tls-client-config
