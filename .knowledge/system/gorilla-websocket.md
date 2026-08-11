---
id: system:gorilla-websocket
type: system
title: gorilla/websocket
---
Upstream WebSocket library forked by requirement:websocket-fork. RFC 6455 client and server, archived upstream but stable at v1.5.3.

```yaml
version: v1.5.3
upstream: github.com/gorilla/websocket
dependencies: none; SOCKS5 proxy support is vendored as x_net_proxy.go
assembly: none, so no noasm tag is needed; decision:fasthttp-zstd-backend removed the last need for one anywhere
uses_from_stdlib: >
  bufio, compress/flate, crypto/rand, crypto/sha1, encoding/base64, net/http,
  net/http/httptrace, net/url, unicode/utf8, unsafe
server_contract: >
  Upgrader.Upgrade needs only http.ResponseWriter plus http.Hijacker; it writes
  the 101 to the hijacked net.Conn itself. That single requirement is what
  rule:nethttp-hijack-deadlock breaks
exports_useful_to_us: IsWebSocketUpgrade(*http.Request) bool
tinygo_gaps:
  http.ProxyFromEnvironment: client.go:142; tinygo http.Transport is an empty struct
  tls.Client_and_ConnectionState: client.go:352
  tls.Config.Clone: client.go:433
  tls.Conn_as_a_type: tls_handshake.go
unsafe_path_is_safe: >
  maskBytes does uintptr word arithmetic; verified byte-exact under tinygo for
  every payload length 0..600, for deliberately unaligned slices, and at frame
  boundaries 125, 126, 127, 65535, 65536
upstream_behaviour_to_know:
  utf8: >
    payload UTF-8 is not validated; doc.go puts that on the application. Only
    close-frame text is checked
  mask_keys: >
    math/rand, not crypto/rand. Auto-seeded on tinygo too, so keys do vary per
    process. Identical on both compilers, so not a tinygo concern
alternatives_not_evaluated: >
  coder/websocket needs Hijacker the same way; gobwas/ws takes a raw net.Conn
  and would not need the bypass ResponseWriter. Neither measured
```
