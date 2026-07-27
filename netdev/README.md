# netdev — host Netdever for TinyGo

TinyGo’s `net` / `net/http` packages do not talk to the OS directly. They call a **Netdever** registered with `UseNetdev`. On microcontrollers that is usually Wi-Fi firmware (WiFiNINA, etc.). On a desktop host there is no default driver, which produces:

```text
Netdev not set
```

This package implements a Netdever on **Linux / macOS / Windows** using the host TCP/IP stack, compatible with the interface in [tinygo-org/drivers/netdev](https://github.com/tinygo-org/drivers/tree/dev/netdev).

## Usage

Blank-import the package before any `net` / `net/http` use (init registers the driver under TinyGo):

```go
package main

import (
	"net/http"

	_ "github.com/shibukawa/tinygodriver/netdev"
)

func main() {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})
	http.ListenAndServe(":8080", nil)
}
```

Or register explicitly:

```go
import "github.com/shibukawa/tinygodriver/netdev"

func main() {
	netdev.Use(netdev.New())
	// ...
}
```

## Build

With TinyGo:

```bash
tinygo build -o server ./examples/httpserver
./server
```

Standard `go build` / `go run` also work: the driver is a no-op registrar because the stock `net` package does not use netdev.

```bash
go run ./examples/httpserver
```

## Notes

- IPv4 only (matches TinyGo’s net port).
- Unix domain sockets (`unix`, `unixgram`, `unixpacket`) are not supported and
  will not be. TinyGo’s `net` rejects those networks before a driver is
  consulted, and the Netdever interface addresses sockets as `netip.AddrPort`,
  which cannot carry a filesystem path. Use `tcp4` on `127.0.0.1` for local IPC.
- Listening on port 0 works, but `net.Listener.Addr()` still reports port 0.
  The Netdever `Bind` signature returns only an error, so the port the OS picked
  cannot be handed back to TinyGo’s `net`. Read it from the driver instead:

  ```go
  d := netdev.New()
  netdev.Use(d)
  fd, _ := d.Socket(netdev.AF_INET, netdev.SOCK_STREAM, netdev.IPPROTO_TCP)
  d.Bind(fd, netip.MustParseAddrPort("127.0.0.1:0"))
  laddr, _ := d.LocalAddr(fd) // 127.0.0.1:54321
  ```

  A full fix needs an upstream change in TinyGo’s `listenTCP`.
- Socket errors are reported as the shared classes in `netdev.Err*`
  (`ErrConnRefused`, `ErrAddrNotAvailable`, …), so `errors.Is` works the same on
  Linux, macOS, and Windows.
- `IPPROTO_TLS` uses OpenSSL 3 on macOS. Peer certificates and hostnames are
  always verified against OpenSSL's default trust paths. Set `SSL_CERT_FILE`
  when a private CA is required.
- Standard Go Linux builds can exercise the same OpenSSL adapter. TinyGo Linux
  cannot safely call the distribution's shared OpenSSL because it bypasses
  glibc process initialization, so TinyGo Linux and Windows currently return
  `ErrProtocolNotSupported` for `IPPROTO_TLS`.
- OpenSSL 3 is a build and runtime dependency (`libssl-dev` on Debian/Ubuntu,
  `openssl@3` with Homebrew on macOS).
- DNS uses `/etc/hosts` (or the Windows hosts file) plus a simple UDP A-record resolver.
- Multiple goroutines may call socket methods concurrently; the driver serializes bookkeeping and relies on OS sockets for I/O.
