---
id: requirement:httpserver-package
type: requirement
title: httpserver Package for Protocol Upgrades
---
Provide a package that lets a TinyGo `net/http` server complete a protocol upgrade, which rule:nethttp-hijack-deadlock makes impossible today. Applications keep `*http.Server` and `http.Handler`; only the serve call changes.

```yaml
priority: must
import_path: github.com/shibukawa/tinygodriver/httpserver
layout: repository root, flat, matching decision:package-layout
design: decision:httpserver-demux
scope:
  in:
    - Serve and ListenAndServe taking the caller's *http.Server
    - a ResponseWriter implementing http.Hijacker for the bypass path
    - configurable bypass predicate, defaulting to the Connection upgrade token
    - head-read deadline, defaulting from http.Server.ReadHeaderTimeout
    - 501 guard on the http.Server path, per decision:httpserver-demux
  out:
    - a new server type or configuration surface; http.Server stays the surface
    - HTTP/2, TLS termination, and anything requirement:no-crypto-tls-on-tinygo bars
    - full ResponseWriter behaviour on the bypass path; Flush, ReadFrom and
      CloseNotify are out, that path exists to be hijacked
    - inspecting later requests on a reused connection
host_go_policy: >
  std path calls srv.Serve(ln) and nothing else, per requirement:std-go-delegation
  and rule:build-tag-selection. Identical public API under every tag combination
acceptance:
  - one source builds under go and tinygo and serves both an upgrade endpoint and
    ordinary endpoints on one port
  - keep-alive works on the ordinary endpoints under tinygo; two sequential
    requests on one connection get two responses
  - the head-read deadline expires a client that stops mid-header
  - an upgrade reaching the http.Server path answers 501 instead of hanging
  - force_tinygo_logic runs the tinygo path under host go, per
    requirement:test-strategy
  - README states why the package exists and when it stops being needed
risk:
  scope_creep: >
    every gap in the bypass ResponseWriter invites reimplementing http.Server.
    Hold the line: bypass is for hijack, everything else goes to http.Server
  first_request_only: accepted and mitigated, see decision:httpserver-demux
depends_on: system:tinygo-netdev, rule:tinygo-readresponse-nil-req, rule:tinygo-test-constraints
proves: requirement:websocket-fork
```
