---
id: system:fasthttp-websocket
type: system
title: fasthttp/websocket (upstream)
---
Upstream WebSocket library for fasthttp, an independently maintained fork of gorilla/websocket that adds `FastHTTPUpgrader` alongside gorilla's `net/http` `Upgrader`. Cannot be used with decision:fasthttp-fork because it imports `github.com/valyala/fasthttp` directly; that import is the reason requirement:fasthttp-websocket-fork exists.

```yaml
module: github.com/fasthttp/websocket
version_pinned: v1.5.12
license: BSD-3-Clause, Gorilla WebSocket Authors; same flavour as system:fasthttp-router, distinct from fasthttp MIT and repo Apache-2.0
packages:
  root: one flat package; _examples dropped
surface:
  fasthttp_side: server_fasthttp.go -- FastHTTPUpgrader, FastHTTPHandler, FastHTTPIsWebSocketUpgrade
  nethttp_side: server.go -- gorilla's Upgrader, unusable on TinyGo per rule:nethttp-hijack-deadlock
  shared: conn.go framing, mask.go, compression.go, prepared.go, client.go Dialer
deps:
  valyala/fasthttp: declared v1.58.0; fork tracks v1.73.0, compile surface is RequestCtx plus status helpers
  klauspost/compress/flate: permessage-deflate; already a repo dependency and already linked by fasthttp
  savsgio/gotils: strconv B2S only
  golang.org/x/net/proxy: SOCKS5 for the client; gorilla vendored this, upstream imports it
stdlib_nontest: bufio, bytes, crypto/rand, crypto/sha1, crypto/tls, encoding/base64, encoding/binary, encoding/json, errors, io, math/rand, net, net/http, net/url, strings, sync, time, unicode/utf8
tinygo_risk:
  crypto_tls: client.go only -- tls.Client panics, tls.Conn is not a type, Config has no Clone
  nethttp: http.ProxyFromEnvironment and http.NewResponseController are absent
  server_side: none; FastHTTPUpgrader and all framing compile as vendored
test_coverage_upstream:
  fasthttp_side: none at all -- no vendored test names FastHTTPUpgrader
  nethttp_side: gorilla's suite, of which client_server_test.go needs cookiejar and TLS httptest
```
