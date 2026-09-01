---
id: requirement:websocket-fork
type: requirement
title: gorilla/websocket Fork for TinyGo
---
Provide a fork of system:gorilla-websocket that builds and runs under TinyGo, so applications get RFC 6455 instead of hand-rolled framing. Four missing symbols block compilation; rule:nethttp-hijack-deadlock blocks the server at runtime and is fixed by requirement:httpserver-package, not here.

```yaml
priority: must
design: decision:websocket-fork
scope:
  in:
    - root package, two patch sites shimmed, one import path, both compilers
    - client over ws and wss, server behind requirement:httpserver-package
    - upstream behaviour preserved on host Go
  out:
    - behaviour changes or new features over upstream v1.5.3
    - the examples/ directory and the vendored SOCKS5 proxy beyond what compiles
    - wss server; tinygo has no ServeTLS and no tls.Server, terminate in front
    - fixing the hijack deadlock, which belongs to requirement:httpserver-package
tls_policy:
  client: >
    wss works only through Dialer.NetDialTLSContext returning an
    already-handshaken connection, net.DialTLS on tinygo. Without it the fork
    must refuse with ErrTLSUnsupported, never panic, because tls.Client panics
  anchors: >
    darwin extra anchors come from SSL_CERT_FILE via netdev. Host Go on darwin
    ignores SSL_CERT_FILE and needs RootCAs passed directly, which matters for
    any test comparing the two
  server: barred, see requirement:no-crypto-tls-on-tinygo
acceptance:
  - fork compiles with tinygo and go from one source, no noasm tag needed
  - conformance battery passes identically on both compilers, covering echo of
    text, binary and empty messages, fragmentation, streaming reads, a 4 MiB
    message, ping and pong both directions, close code and text, read limit,
    permessage-deflate, subprotocol negotiation, origin rejection and concurrency
  - every payload length 0..600 and each frame boundary round-trips byte-exact,
    covering the maskBytes unsafe path
  - wss client round-trips against a TLS server and refuses an untrusted cert
  - README plus PATCHES.md record every divergence
verified: >
  2026-08-10, tinygo 0.41.1 darwin/arm64, prototype in scratch. 27/27 both
  compilers. Measured cost: 305 KB over the same server without it, of which
  compress/flate is 81 KB. Echo throughput tinygo against host Go: 33.8k vs
  38.0k msg/s at 64 B, 23.0k vs 47.1k at 1 KiB, 3.3k vs 4.2k at 64 KiB
risk:
  upstream_archived: >
    gorilla/websocket is archived; v1.5.3 is stable and dependency-free, and the
    fork freezes it anyway, so this lowers rebase risk rather than raising it
  test_deadlocks: >
    a battery that writes a large message before reading deadlocks against an
    echo server once both socket buffers fill. Write from a goroutine
  test_portability: rule:tinygo-test-constraints, rule:vendored-godebug-drift
```
