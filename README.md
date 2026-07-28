# tinygodriver

Networking additions that make TinyGo's standard-library HTTP stack more
useful on desktop hosts.

The repository provides a host network driver plus TinyGo-compatible versions
of newer HTTP APIs that are not yet fully available in TinyGo. The packages
also build with standard Go, which makes it possible to share application code
and tests between both compilers.

## Packages

| Package | Import | Description |
|---------|--------|-------------|
| [`netdev`](./netdev) | `github.com/shibukawa/tinygodriver/netdev` | Host TCP/IP Netdever (BSD sockets + optional OpenSSL TLS) |
| [`https`](./https) | `github.com/shibukawa/tinygodriver/https` | `net/http`-compatible HTTPS client using the OS TLS stack |
| [`httpmux`](./httpmux) | `github.com/shibukawa/tinygodriver/httpmux` | Go 1.22-style `ServeMux` patterns for TinyGo |
| [`httprevproxy`](./httprevproxy) | `github.com/shibukawa/tinygodriver/httprevproxy` | TinyGo-compatible subset of `net/http/httputil.ReverseProxy` |

## Quick start

Blank-import `netdev` before using `net` or `net/http` on a desktop TinyGo
build. Use `httpmux` when the application needs method-aware patterns or path
wildcards:

```go
package main

import (
	"fmt"
	"net/http"
	"os"

	"github.com/shibukawa/tinygodriver/httpmux"
	_ "github.com/shibukawa/tinygodriver/netdev"
)

func main() {
	mux := httpmux.NewServeMux()
	mux.HandleFunc("GET /users/{id}", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "user=%s\n", r.PathValue("id"))
	})
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}
	fmt.Println("listening on", addr)
	http.ListenAndServe(addr, mux)
}
```

### Run the example

The included server exercises `netdev`, `httpmux`, and `httprevproxy` together.
Its `/proxy/{path...}` route forwards requests to `UPSTREAM_URL` (default
`http://127.0.0.1:8081`).

```bash
tinygo build -o server ./examples/httpserver
./server
# curl http://127.0.0.1:8080/healthz
# curl http://127.0.0.1:8080/proxy/users/42
```

Standard `go run` also works. In a standard Go build, `netdev` registration is
a no-op and `httpmux.ServeMux` is an alias of `net/http.ServeMux`.

```bash
go run ./examples/httpserver
```

### Explicit netdev registration

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
| HTTP server and reverse proxy | [`examples/httpserver`](./examples/httpserver) | Method-aware routes, host netdev, and a configurable reverse proxy |
| HTTPS client | [`examples/httpsclient`](./examples/httpsclient) | `https.Get` over the OS TLS stack, with an optional custom CA |
| HTTPS platform demo | [`examples/httpsdemo`](./examples/httpsdemo) | One source, every platform: verifies trust, refusal behavior, and deadlines |

## Platform notes

See the package READMEs for detailed API behavior and limitations:

- [`netdev`](./netdev/README.md): TLS (OpenSSL), DNS, and platform notes
- [`https`](./https/README.md): HTTPS client backends, configuration, and limitations
- [`httpmux`](./httpmux/README.md): supported patterns and implementation selection
- [`httprevproxy`](./httprevproxy/README.md): proxy features and unsupported protocols

- **IPv4 only** (matches TinyGo’s net port).
- **HTTPS client (`https`)**: Network.framework on macOS, Schannel on Windows,
  and vendored mbedTLS on Linux, all with **no external TLS library to
  install**. Other TinyGo targets return `ErrPlatformNotSupported`; standard Go
  builds delegate to `net/http`.
- **`netdev` TLS (`IPPROTO_TLS`)**: Secure Transport on macOS and Schannel on
  Windows, neither of which needs a package manager. TinyGo on Linux returns
  `ErrProtocolNotSupported`; host-Go Linux uses OpenSSL 3 (`libssl-dev` on
  Debian/Ubuntu). The `https` package needs none of this.

## Install

Install only the packages an application uses, for example:

```bash
go get github.com/shibukawa/tinygodriver/netdev@latest
go get github.com/shibukawa/tinygodriver/httpmux@latest
go get github.com/shibukawa/tinygodriver/httprevproxy@latest
```

Requires [TinyGo](https://tinygo.org/getting-started/install/) for TinyGo builds. Compatible with the Netdever interface in [tinygo-org/drivers/netdev](https://github.com/tinygo-org/drivers/tree/dev/netdev).

## License

[Apache License 2.0](./LICENSE)
