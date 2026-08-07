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
| [`netdev`](./netdev) | `github.com/shibukawa/tinygodriver/netdev` | Host TCP/IP Netdever (BSD sockets + OS-native TLS) |
| [`https`](./https) | `github.com/shibukawa/tinygodriver/https` | `net/http`-compatible HTTPS client using the OS TLS stack |
| [`httpmux`](./httpmux) | `github.com/shibukawa/tinygodriver/httpmux` | Go 1.22-style `ServeMux` patterns for TinyGo |
| [`httprevproxy`](./httprevproxy) | `github.com/shibukawa/tinygodriver/httprevproxy` | TinyGo-compatible subset of `net/http/httputil.ReverseProxy` |
| [`sqlite`](./database/sql/sqlite) | `github.com/shibukawa/tinygodriver/database/sql/sqlite` | SQLite `database/sql` driver, backend chosen at build time |
| [`pgx`](./database/pgx) | `github.com/shibukawa/tinygodriver/database/pgx` | pgx-native PostgreSQL API: Batch, CopyFrom, LISTEN/NOTIFY; TLS included |
| [`pgx/pgxpool`](./database/pgx/pgxpool) | `github.com/shibukawa/tinygodriver/database/pgx/pgxpool` | Concurrency-safe pool of native pgx connections |
| [`pgx/stdlib`](./database/pgx/stdlib) | `github.com/shibukawa/tinygodriver/database/pgx/stdlib` | PostgreSQL `database/sql` driver built on pgx |
| [`mysql`](./database/sql/mysql) | `github.com/shibukawa/tinygodriver/database/sql/mysql` | MySQL and MariaDB `database/sql` driver built on go-sql-driver, TLS included |
| [`storage/s3`](./storage/s3) | `github.com/shibukawa/tinygodriver/storage/s3` | S3 client (SigV4), where `aws-sdk-go-v2` does not build |
| [`nosql/dynamodb`](./nosql/dynamodb) | `github.com/shibukawa/tinygodriver/nosql/dynamodb` | DynamoDB client, JSON protocol, retries and pooled connections |
| [`nosql/datastore`](./nosql/datastore) | `github.com/shibukawa/tinygodriver/nosql/datastore` | Firestore in Datastore mode client, where neither Google Go client builds |
| [`cloud/aws`](./cloud/aws) | `github.com/shibukawa/tinygodriver/cloud/aws` | SigV4 signing and credentials, shared by the AWS clients |
| [`cloud/google`](./cloud/google) | `github.com/shibukawa/tinygodriver/cloud/google` | Google credentials and bearer tokens, RSA signed by the OS on TinyGo |
| [`jwt`](./jwt) | `github.com/shibukawa/tinygodriver/jwt` | Bounded signed JWT subset, HS256 and RS256 |

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
| S3 object storage | [`examples/s3demo`](./examples/s3demo) | Put, get, range-read, list, and delete against any S3-compatible endpoint |
| DynamoDB | [`examples/dynamodbdemo`](./examples/dynamodbdemo) | Table lifecycle, conditional writes, batches, and paged queries |
| Datastore | [`examples/datastoredemo`](./examples/datastoredemo) | Typed round trip, ancestor query over two pages, and a transaction |

## Platform notes

See the package READMEs for detailed API behavior and limitations:

- [`netdev`](./netdev/README.md): TLS, DNS, and platform notes
- [`https`](./https/README.md): HTTPS client backends, configuration, and limitations
- [`httpmux`](./httpmux/README.md): supported patterns and implementation selection
- [`httprevproxy`](./httprevproxy/README.md): proxy features and unsupported protocols
- [`storage/s3`](./storage/s3/README.md): supported operations, configuration, and limitations
- [`nosql/dynamodb`](./nosql/dynamodb/README.md): attribute values, pagination, retries and what a retry can deliver twice
- [`nosql/datastore`](./nosql/datastore/README.md): values, keys, queries, conditional writes and contention
- [`cloud/aws`](./cloud/aws/README.md): signing another AWS service with this signer
- [`cloud/google`](./cloud/google/README.md): token sources, clock skew, and where the RSA code goes

- **IPv4 only** (matches TinyGo’s net port).
- **HTTPS client (`https`)**: Network.framework on macOS, Schannel on Windows,
  and vendored mbedTLS on Linux, all with **no external TLS library to
  install**. Other TinyGo targets return `ErrPlatformNotSupported`; standard Go
  builds delegate to `net/http`.
- **`netdev` TLS (`IPPROTO_TLS`)**: Secure Transport on macOS, Schannel on
  Windows and `crypto/tls` on host-Go Linux, none of which needs a package
  manager. TinyGo on Linux returns `ErrProtocolNotSupported`. The `https`
  package needs none of this.

## Install

Install only the packages an application uses, for example:

```bash
go get github.com/shibukawa/tinygodriver/netdev@latest
go get github.com/shibukawa/tinygodriver/httpmux@latest
go get github.com/shibukawa/tinygodriver/httprevproxy@latest
go get github.com/shibukawa/tinygodriver/storage/s3@latest
go get github.com/shibukawa/tinygodriver/nosql/dynamodb@latest
```

Requires [TinyGo](https://tinygo.org/getting-started/install/) for TinyGo builds. Compatible with the Netdever interface in [tinygo-org/drivers/netdev](https://github.com/tinygo-org/drivers/tree/dev/netdev).

## License

[Apache License 2.0](./LICENSE)
