---
id: requirement:nethttp-compatible-client
type: requirement
title: net/http-Compatible Client Surface
---
The package must be usable by replacing the `http.` prefix with `https.` in client call sites, with no change to request or response handling code.

```yaml
priority: must
acceptance:
  - Get, Head, Post, PostForm exist with net/http-identical signatures
  - returned type is *net/http.Response, not a package-local type
  - Transport satisfies net/http.RoundTripper and drops into http.Client
  - a compile-time assertion proves signature parity against net/http
  - the same test file compiles against both net/http and this package
excluded_from_parity:
  - Do, ServeMux, and other server-side surface
  - http.Client itself; NewClient returns *http.Client rather than a new type
verified_by: requirement:test-strategy
implemented_by:
  - api:https-functions
  - api:https-transport
