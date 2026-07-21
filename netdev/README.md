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
