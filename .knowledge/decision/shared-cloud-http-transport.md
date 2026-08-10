---
id: decision:shared-cloud-http-transport
type: decision
title: AWS And Google Share HTTP Transport Plumbing
---
Cloud authentication stays provider-specific; identical HTTP client construction belongs to one internal package.

```yaml
state: accepted
accepted_on: 2026-08-10
implemented_on: 2026-08-10
trigger: cloud/aws and cloud/google duplicate ClientOptions, NewHTTPClient, CloseIdleConnections, and both build adapters
boundary:
  shared_internal:
    - resolved transport options
    - standard and native transport construction
    - idle-connection closing
  provider_specific:
    - credentials and environment resolution
    - request signing
    - bearer tokens and refresh
public_api: cloud/aws and cloud/google may retain aliases or thin forwards for compatibility
implementation: internal/cloudhttp owns both build adapters and lifecycle helpers; provider packages are thin forwards
reason: transport behavior fixes and defaults must land once
not_a_goal: one abstraction over AWS signing and Google authentication
related:
  - decision:aws-shared-package
  - decision:google-shared-package
  - decision:http-client-policy-ownership
  - requirement:configuration-semantics-parity
```
