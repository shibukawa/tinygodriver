---
id: requirement:configuration-semantics-parity
type: requirement
title: Configuration Meaning Is Backend Independent
---
One public setting must retain the same scope, zero-value meaning, and precedence on every build path.

```yaml
priority: must
state: shipped 2026-08-10
contract:
  DialTimeout: bounds TCP connection setup and TLS handshake
  ResponseTimeout: bounds response headers and body
  IdleConnTimeout_zero: 20s
  MaxIdleConnsPerHost_zero: 2
  request_context: an earlier context deadline always wins
implementation:
  - resolve defaults before selecting a backend
  - adapters receive resolved non-zero values
  - standard Go must configure both dialing and TLS, not TLSHandshakeTimeout alone
  - response-body timeout must not collapse to ResponseHeaderTimeout
acceptance:
  - identical black-box timeout tests pass with and without force_tinygo_logic
  - zero-value pool expiry is asserted on both paths
  - comments name logical scope rather than backend mechanism
verification: the same black-box timeout and default tests cover both build paths
related:
  - requirement:deadline-support
  - requirement:std-go-delegation
```
