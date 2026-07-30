# mysql

This package registers a MySQL and MariaDB `database/sql` driver as `mysql` and
selects its implementation at build time:

| Build | Backend |
| --- | --- |
| TinyGo or `-tags force_tinygo_logic` | Petitweb [`tinygomysql`](./tinygomysql) |
| Standard Go | `github.com/go-sql-driver/mysql` |

```go
import "github.com/shibukawa/tinygodriver/database/sql/mysql"

db, err := mysql.Open("user:pass@tcp(127.0.0.1:3306)/app")
```

The DSN syntax, driver name and `database/sql` behavior are the same on both
backends, because the TinyGo backend is a fork of the standard-Go one. It exists
because TinyGo's `net.Conn` cannot supply a descriptor for connection health
checks or for an in-band TLS upgrade; see
[tinygomysql/README.md](./tinygomysql/README.md) for the details and for the
MPL-2.0 notice that covers that directory.

## TLS

`tls=true`, `tls=skip-verify` and `tls=preferred` work on both backends. For a
private CA, register the trust settings under a name and use it as `tls=<name>`:

```go
ca, err := os.ReadFile("/etc/ssl/db-ca.pem")
if err != nil {
	log.Fatal(err)
}
err = mysql.RegisterTLSConfig("db", &https.Config{
	RootCAs:     [][]byte{ca},
	RootCAsOnly: true,
})
// ...
db, err := mysql.Open("user:pass@tcp(db.internal:3306)/app?tls=db")
```

`RegisterTLSConfig` takes an `https.Config` on both backends, so this code is
portable; the standard-Go backend converts it to a `crypto/tls.Config`
internally. PEM bytes are used rather than `crypto/tls` types because TinyGo
builds must not link `crypto/tls`.

On TinyGo the handshake runs on the OS TLS stack through the `https.DialPlain` /
`https.Upgrade` seam — Secure Transport on macOS, mbedTLS on Linux, Schannel on
Windows. macOS caps at TLS 1.2 there, because Apple never added 1.3 to Secure
Transport; build with `-tags darwinstarttlswith13` for 1.3. Client certificates
are unsupported on macOS.

## TinyGo notes

Use the threads scheduler, which is the default on desktop targets. Under the
cooperative scheduler (`-scheduler=tasks`) a blocking socket call holds the
whole runtime, so the driver's cancellation watcher never runs and
`QueryContext` ignores its deadline without reporting an error. Measured: a
`SELECT SLEEP(5)` with a 500 ms deadline returned after the full five seconds
with a nil error.

Blank-import `netdev`, as with any TinyGo program that uses the network:

```go
import _ "github.com/shibukawa/tinygodriver/netdev"
```

Unix sockets and IPv6 are unavailable, so connect over TCP to an IPv4 host. The
DSN `timeout=` parameter has no effect, because TinyGo's `net.Dialer` ignores
both its `Timeout` field and the context. `readTimeout`, `writeTimeout` and
query-level deadlines all work.
