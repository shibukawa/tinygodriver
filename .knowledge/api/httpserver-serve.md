---
id: api:httpserver-serve
type: api
title: httpserver Serve API
---
Serve entry points for requirement:httpserver-package. The caller keeps `*http.Server`, so timeouts, handler and shutdown stay where they already are.

```yaml
signatures:
  - func Serve(ln net.Listener, srv *http.Server) error
  - func ListenAndServe(addr string, h http.Handler) error
  - func ServeConfig(ln net.Listener, srv *http.Server, cfg Config) error
config:
  ShouldBypass: >
    func(*http.Request) bool; nil means the default, Connection carries the
    upgrade token. Return true to reach the handler over a hijackable
    ResponseWriter
  ReadHeaderTimeout: >
    time.Duration bounding the head read; zero inherits http.Server's, and if
    that is zero too a package default applies rather than no bound
behaviour:
  std_path: Serve calls srv.Serve(ln); Config is accepted and ignored except that
    ReadHeaderTimeout still feeds srv.ReadHeaderTimeout
  tinygo_path: decision:httpserver-demux
bypass_responsewriter:
  implements: http.ResponseWriter and http.Hijacker
  not_implemented: Flush, ReadFrom, CloseNotify, trailers, chunked encoding
  before_hijack: >
    Header and Write work; the response is buffered so Content-Length is
    accurate. After Hijack the writer is inert and the connection is the
    handler's
errors:
  head_too_large: connection closed, no response; bound is a package constant
  head_timeout: connection closed, no response
  upgrade_via_http_server: 501 with a body naming rule:nethttp-hijack-deadlock
notes: >
  Serve owns the listener lifecycle; srv.Shutdown still governs the connections
  http.Server was given, but not one already hijacked, which is standard
  net/http behaviour
```
