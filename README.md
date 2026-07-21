# tinygodriver

Host-side network drivers for [TinyGo](https://tinygo.org/).

TinyGo’s `net` / `net/http` packages do not talk to the OS directly. They call a **Netdever** registered with `UseNetdev`. On microcontrollers that is usually Wi-Fi firmware. On a desktop host there is no default driver, which produces:

```text
Netdev not set
```

This repository provides desktop Netdever implementations so the same TinyGo networking code can run on Linux, macOS, and Windows.

## Packages

| Package | Import | Description |
|---------|--------|-------------|
| [`netdev`](./netdev) | `github.com/shibukawa/tinygodriver/netdev` | Host TCP/IP Netdever (BSD sockets + optional OpenSSL TLS) |

## Quick start

Blank-import the driver before any `net` / `net/http` use. Under TinyGo, `init` registers the host driver automatically:

```go
package main

import (
	"fmt"
	"net/http"
	"os"

	_ "github.com/shibukawa/tinygodriver/netdev"
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "hello method=%s path=%q\n", r.Method, r.URL.Path)
	})
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}
	fmt.Println("listening on", addr)
	http.ListenAndServe(addr, mux)
}
```

### Build with TinyGo

```bash
tinygo build -o server ./examples/httpserver
./server
# curl http://127.0.0.1:8080/
```

Standard `go build` / `go run` also work: the driver is a no-op registrar because stock Go’s `net` package does not use netdev.

```bash
go run ./examples/httpserver
```

### Explicit registration

```go
import "github.com/shibukawa/tinygodriver/netdev"

func main() {
	netdev.Use(netdev.New())
	// ...
}
```

## Examples

| Example | Path | Description |
|---------|------|-------------|
| HTTP server | [`examples/httpserver`](./examples/httpserver) | Minimal `net/http` server using the host netdev |

## Platform notes

See [`netdev/README.md`](./netdev/README.md) for TLS (OpenSSL), DNS, and platform limitations.

- **IPv4 only** (matches TinyGo’s net port).
- **TLS (`IPPROTO_TLS`)**: OpenSSL 3 on macOS (and standard Go on Linux). TinyGo on Linux and Windows currently return `ErrProtocolNotSupported` for TLS.
- OpenSSL 3 is required for TLS builds (`openssl@3` via Homebrew on macOS, `libssl-dev` on Debian/Ubuntu).

## Install

```bash
go get github.com/shibukawa/tinygodriver/netdev@latest
```

Requires [TinyGo](https://tinygo.org/getting-started/install/) for TinyGo builds. Compatible with the Netdever interface in [tinygo-org/drivers/netdev](https://github.com/tinygo-org/drivers/tree/dev/netdev).

## License

[Apache License 2.0](./LICENSE)
