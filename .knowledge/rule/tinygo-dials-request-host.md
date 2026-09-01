---
id: rule:tinygo-dials-request-host
type: rule
title: TinyGo Dials Request.Host, Not Request.URL.Host
---
TinyGo's HTTP client takes the dial address from `Request.Host`; standard net/http takes it from `Request.URL.Host` and treats `Request.Host` as the Host header alone. Any code that sets one without the other goes to the wrong place, or nowhere.

```yaml
scope: every TinyGo caller of http.Client or http.Transport that rewrites a request
measured: 2026-09-02, tinygo 0.42.0 darwin/arm64
mechanism: >
  src/net/http/client.go roundTrip, marked "copied from Go 1.21.4", does
  `host := req.Host` and dials that. Standard net/http dials canonicalAddr(
  treq.URL). The two fields are independent upstream and collapsed here
failure_modes:
  empty_host:
    who: anything using httputil-style Rewrite plus ProxyRequest.SetURL
    why: >
      SetURL sets Out.Host = "" on purpose, which is how net/http spells "take
      the Host header from the URL". TinyGo reads it as an empty dial address
    symptom: net.DialTCP fails with "invalid IP address"; a proxy answers 502
  host_disagrees_with_url:
    who: httputil-style Director, including NewSingleHostReverseProxy
    why: Director preserves the inbound Host, which is the proxy's own address
    symptom: >
      the proxy dials itself and loops until the header limit trips. Observed as
      431 plus a connection reset, not as an error naming the cause
host_go_cannot_catch_either: >
  both are correct on standard Go, so a suite that runs only `go test` and
  `go test -tags force_tinygo_logic` passes while the tinygo binary fails. This
  is rule:build-tag-selection test_tags in a different guise: the behaviour
  under test only exists on the compiler the test was not running on
rules:
  - >
    fill an empty Request.Host from Request.URL.Host before RoundTrip on the
    tinygo path. That is byte-for-byte the Host header standard net/http would
    have written, so nothing changes on the wire
  - >
    report, do not guess, when Host and URL.Host disagree: only one of the two
    can be honoured and picking either silently misroutes
  - a regression test for this must run under `tinygo test` over real sockets
applied_in: >
  httprevproxy/outboundhost_tinygo.go, with the no-op std half beside it. The
  stdlib net/http/httputil.ReverseProxy that tinygo 0.42 now ships fails
  identically, so this is a platform rule, not a fork defect
verify: >
  proxy through Rewrite + SetURL under `tinygo test` and assert the backend saw
  the request; httprevproxy/proxy_integration_test.go is the committed form
```
