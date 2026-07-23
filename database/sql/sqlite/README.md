# sqlite

This package registers a SQLite `database/sql` driver as `sqlite` and selects
its implementation at build time:

| Build | Backend |
| --- | --- |
| TinyGo or `-tags force_tinygo_logic` | Petitweb `tinygosqlite` |
| Standard Go with cgo | `github.com/mattn/go-sqlite3` |
| Standard Go without cgo | `modernc.org/sqlite` |

```go
import "github.com/shibukawa/tinygodriver/database/sql/sqlite"

db, err := sqlite.Open("app.db")
```

For portable applications, use a plain filename, `:memory:`, or a `file:` URI
limited to SQLite's `mode`, `cache`, and `immutable` parameters. The Petitweb
native backend additionally accepts `busy_timeout` in milliseconds.

See [BENCHMARK.md](BENCHMARK.md) for a reproducible host-backend baseline.

PostgreSQL and MySQL are not first-class Petitweb targets. Their maintained Go
drivers and secure TLS stacks do not currently form a reliable TinyGo-compatible
path, so generators, examples, and compatibility guarantees do not cover them.
