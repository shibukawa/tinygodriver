# httpserver

Serves `net/http` handlers on TinyGo when one of them needs to take over the
connection.

```go
import "github.com/shibukawa/tinygodriver/httpserver"
```

## Why this exists

TinyGo's `net/http` server cannot complete a protocol upgrade. Before calling a
handler it starts a background read on the connection, and it cancels that read
by moving the read deadline into the past. netdev takes a deadline by value
when a read begins, so it cannot interrupt a `recv()` already in flight: the
cancellation never lands and `Hijack` blocks forever.

A WebSocket handshake served this way hangs with no error, no panic and no log
line. The client times out; the server logs nothing.

This package reads the request head itself and decides where each connection
goes. Anything that is not an upgrade is handed to a real `http.Server`, with
the head replayed, so keep-alive, timeouts and graceful shutdown keep working.
An upgrade reaches the handler through a `ResponseWriter` that implements
`http.Hijacker`, with no background read in the way.

Under standard Go, `Serve` calls `srv.Serve(ln)` and nothing else. `net/http`
can hijack there, so none of this is needed and none of it runs.

## Usage

One listener, one port. A WebSocket endpoint is one route among many.

```go
mux := http.NewServeMux()
mux.HandleFunc("/healthz", healthz)
mux.HandleFunc("/ws", serveWebSocket) // calls websocket.Upgrader.Upgrade

ln, err := net.Listen("tcp", ":8080")
if err != nil {
	return err
}
return httpserver.Serve(ln, &http.Server{Handler: mux})
```

`srv.Shutdown` and `srv.Close` still work: the package serves the caller's own
`http.Server` rather than a copy, so the connections it hands over stay under
that server's control.

### Configuration

`ServeConfig` takes a `Config`:

| Field | Meaning |
|---|---|
| `ShouldBypass` | Which requests need a hijackable connection. `nil` means `IsUpgrade`, which matches the `Connection: upgrade` token and so covers WebSocket without naming it. |
| `ReadHeaderTimeout` | Bounds the read of the request head. Zero takes `http.Server.ReadHeaderTimeout`, then `DefaultReadHeaderTimeout` (10s). Negative means no limit. |

`net/http` defaults to no header timeout; this package does not, because
reading the head is its own job here and an unbounded read is a goroutine a
stalled client can hold forever.

Keep `ShouldBypass` narrow. A request it accepts reaches a `ResponseWriter`
that implements `Header`, `Write`, `WriteHeader` and `Hijack` and nothing else
— no `Flush`, no `ReadFrom`, no `CloseNotify`, no trailers, no chunked
encoding. That writer exists to be hijacked. A handler that needs the rest is
not an upgrade handler and should go to `http.Server`.

## Limits

**Only the first request on a connection is inspected.** A browser opens a
fresh connection for a WebSocket handshake, so this holds in practice. An
upgrade arriving as a later request on a reused connection is answered `501
Not Implemented` rather than deadlocking. Inspecting every request would mean
reimplementing `http.Server`, which this package deliberately does not do.

**An upgrade request must carry no body and no early data.** A client that
sends some is answered `400`: the hijacked reader starts at the connection, not
at those leftover bytes, so accepting them would drop them silently. RFC 6455
forbids a body in the handshake, so this should never fire for WebSocket.

**Plaintext only.** TinyGo's `http.Server` has no `ServeTLS` and its
`crypto/tls` no `Server` or `X509KeyPair`. Terminate TLS in front of the
process.

## When this package stops being necessary

It works around one upstream defect and nothing else. It becomes unnecessary
the moment TinyGo's `net` makes deadlines live — by re-checking a mutable
deadline, or by polling a non-blocking socket — or TinyGo's `net/http` stops
starting the background read. Either fix makes plain `http.Server` work, and
callers can drop the `Serve` call for `srv.Serve`.

The defect is not fixable in netdev: the `Netdever` interface passes the
deadline by value per call, so no driver change can interrupt a call already in
flight. It is the same root cause that makes `SetDeadline`-based query
cancellation ineffective for the PostgreSQL driver.

## Implementation selection

Follows the repository convention: `server_std.go` carries
`!tinygo && !force_tinygo_logic`, `server_tinygo.go` and
`bypasswriter_tinygo.go` carry `tinygo || force_tinygo_logic`. Building with
`-tags force_tinygo_logic` runs the TinyGo path under host Go, which is how
most of the test suite is exercised without a TinyGo toolchain.

`Backend` reports which path was selected, `"std"` or `"tinygo"`.

## Tests

```bash
go test ./httpserver                            # std path
go test -tags force_tinygo_logic ./httpserver   # TinyGo path, host toolchain
tinygo test ./httpserver                        # TinyGo path, real compiler
```

Two TinyGo facts shape the tests, and any test added here: `net.Listener.Addr()`
reports port 0 for a port 0 listen under netdev, so the port must be chosen by
the test; and `t.Fatalf` does not stop the goroutine, so every failure needs an
explicit `return`.
