---
id: requirement:s3-redirect-resigning
type: requirement
title: Redirects Are Followed By The Client, Not By http.Client
---
`http.Client` must not follow redirects for a signed S3 request; `storage/s3` follows them and signs each hop.

```yaml
priority: must
reason:
  - the sigv4 signature covers the host header, so a redirected request needs a new signature
  - S3 answers a region mismatch with 301 PermanentRedirect and x-amz-bucket-region
  - net/http replays a redirect with the original Authorization header, which then fails
tinygo_behaviour: >
  tinygo's Client.do drops redirect handling entirely, so a redirect surfaces to
  the caller. Handling redirects in the driver is therefore not an extra feature,
  it is the only behaviour available on both compilers.
implementation:
  host_go: >
    CheckRedirect returns http.ErrUseLastResponse. The file carries "!tinygo", not
    "!tinygo && !force_tinygo_logic", because the tinygo code path is exercised
    under host go where a standard http.Client would otherwise follow redirects
  tinygo: a no-op, since http.ErrUseLastResponse does not exist there
  driver:
    - Location wins; a region-only redirect retargets the region label of an AWS host
    - the region learned from x-amz-bucket-region is kept for later requests
    - bounded at 3 hops, then ErrTooManyRedirect
    - a body that cannot be replayed reports it instead of sending a truncated request
acceptance:
  - TestRedirectIsResigned asserts the second hop carries a different signature and the new region
  - TestRedirectLoopStops asserts the bound holds under both build configurations
related: rule:build-tag-selection
