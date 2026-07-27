---
id: api:https-functions
type: api
title: Package-Level Request Helpers
---
Mirror of the `net/http` package-level client helpers, restricted to `https` URLs, returning stock `*http.Response`.

```yaml
signatures:
  - func Get(url string) (*http.Response, error)
  - func Head(url string) (*http.Response, error)
  - func Post(url, contentType string, body io.Reader) (*http.Response, error)
  - func PostForm(url string, data url.Values) (*http.Response, error)
  - func NewClient(opts ...Option) *http.Client
  - var DefaultClient *http.Client
semantics:
  types: request and response types are net/http and net/url, never redefined
  default_client: "&http.Client{Transport: DefaultTransport}"
  redirects: handled by http.Client, unchanged from net/http
  body_close: caller closes Response.Body; that close releases the TLS connection
scheme_handling:
  https: routed through api:https-transport
  http: delegated to plain TCP so mixed-scheme redirects keep working
migration: >
  replacing `http.Get` with `https.Get` is the only source change an application
  needs; see flow:https-roundtrip
satisfies: requirement:nethttp-compatible-client
