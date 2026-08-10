---
id: rule:tinygo-readresponse-nil-req
type: rule
title: ReadResponse Needs a Non-nil Request on TinyGo
---
`http.ReadResponse(r, nil)` panics with a nil pointer dereference under TinyGo, though the same file's doc comment promises nil is allowed. Pass a throwaway request.

```yaml
scope: any code parsing an HTTP response off a raw stream with no request in hand
defect:
  observed: "panic: runtime error: nil pointer dereference"
  location: tinygo net/http response.go:201
  code: readTransfer(resp, r, req.onEOF)
  upstream_go: readTransfer(resp, r)
  cause: >
    tinygo added an onEOF hook (transfer.go:484, request.go:340, set from
    client.go:306) so a client can close the connection when the body drains,
    and dereferences req for it without a nil check
  contract_violated: >
    response.go:147 still carries upstream's "The req parameter optionally
    specifies the Request ... If nil, a GET request is assumed"
  host_go: unaffected
workaround: pass http.NewRequest("GET", url, nil) as req
upstream_fix: read req.onEOF into a local only when req != nil, three lines
rules:
  - never pass nil as the req argument on the tinygo path
  - a proxy or demultiplexer parsing responses must keep a request to hand
affects: >
  proxies, protocol demultiplexers, and tests speaking HTTP by hand; found while
  measuring keep-alive for decision:httpserver-demux
measured: 2026-08-10, tinygo 0.41.1 darwin/arm64, reproduced in isolation
report_upstream: true
```
