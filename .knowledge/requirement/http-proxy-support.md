---
id: requirement:http-proxy-support
type: requirement
title: HTTP Proxy Support, Missing on the Native Path
---
The native backends dial the origin server directly and ignore the proxy environment variables, so an application behind a mandatory proxy cannot reach anything.

```yaml
priority: should
state: gap, not implemented
found: 2026-07-30, by a user on a corporate windows network
evidence:
  - grep -i proxy over the non-test sources of https/ and netdev/ returns nothing
  - roundtrip_std.go clones http.DefaultTransport, which carries
    Proxy: ProxyFromEnvironment, so the std path does honor them
asymmetry:
  std_go: HTTP_PROXY, HTTPS_PROXY and NO_PROXY all honored, via net/http
  native: ignored entirely; roundtrip_native.go calls dialTLS straight away
  consequence: >
    plain `go build` works behind a proxy and `go build -tags
    force_tinygo_logic` does not, on the same machine, which reads as a bug in
    the tagged build rather than a missing feature
symptom:
  looks_like: a dial failure naming a socket error, never a proxy
  example: 'https: dial www.google.com: syscall error: winsock error 10013'
  why_confusing: >
    DNS succeeds, the socket is created, and only connect fails, so the first
    suspicion falls on the socket layer. requirement:error-classification made
    this worse until the raw errno was preserved: every unmapped winsock code
    became a bare "syscall error".
  triage: >
    Socket OK plus Connect failing to a public address on 443 is the signature.
    A blocked direct route reports WSAEACCES 10013, WSAENETUNREACH 10051 or
    WSAEHOSTUNREACH 10065, none of which are mapped sentinels.
design_note: >
  the work is a CONNECT tunnel, and api:tls-dialer already exposes the two
  primitives it needs. DialPlain gives a plaintext socket that carries its
  descriptor, and Upgrade starts TLS on a socket that has already carried
  plaintext. CONNECT is exactly that sequence, so no backend needs to change.
  This matters most on darwin, where dial cannot adopt an existing socket but
  the Secure Transport upgrade path can, so even there the feature costs
  nothing new.
open_questions:
  scope: whether to honor the environment variables or take an explicit config field
  auth: whether Proxy-Authorization is in scope
  no_proxy: NO_PROXY matching rules are fiddly; net/http's httpproxy logic is the reference
until_then:
  workaround: build without force_tinygo_logic, which uses net/http and its proxy support
  not_available_to: tinygo builds, which have no std path to fall back to
```
