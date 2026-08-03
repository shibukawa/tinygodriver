//go:build !tinygo && !force_tinygo_logic

package pgxstdlib

import (
	"database/sql"
	"database/sql/driver"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgconn/ctxwatch"
	"github.com/jackc/pgx/v5/stdlib"
)

// backendName identifies the pgx in use, for tests and diagnostics.
const backendName = "upstream"

// The pgx types reachable through WithConn. They are aliases, not definitions,
// so a value obtained here is the pgx value itself and satisfies pgx interfaces.
//
// The set is per build, and the vendored backend defines the same names against
// its own copy. Aliasing is what makes that copy usable at all: it lives under
// internal/, which no package outside pgxstdlib may import, so without these a
// caller could not name the type it just received. See rawconn.go.
type (
	Conn           = pgx.Conn
	Batch          = pgx.Batch
	BatchResults   = pgx.BatchResults
	QueuedQuery    = pgx.QueuedQuery
	Rows           = pgx.Rows
	Row            = pgx.Row
	Identifier     = pgx.Identifier
	CopyFromSource = pgx.CopyFromSource
	CommandTag     = pgconn.CommandTag
	Notification   = pgconn.Notification
	PgError        = pgconn.PgError
)

// The CopyFrom source constructors, as variables because Go has no alias for a
// function.
var (
	CopyFromRows  = pgx.CopyFromRows
	CopyFromSlice = pgx.CopyFromSlice
)

// driverInstance identifies this backend's driver for sqlbatch.Register, which
// keys on the driver's type. stdlib.OpenDB hands that same type to every handle
// Open returns, so one instance is enough to name it.
func driverInstance() driver.Driver { return stdlib.GetDefaultDriver() }

// open uses upstream pgx, which needs no modification on this compiler.
func open(dsn string) (*sql.DB, error) {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	cfg.BuildContextWatcherHandler = cancelRequestWatcher
	return stdlib.OpenDB(*cfg), nil
}

// cancelRequestWatcher replaces pgx's default, which cancels by moving the
// deadline on the in-flight connection. That does not work under TinyGo, and
// using the same handler on both compilers keeps cancellation behavior
// identical rather than platform-dependent.
func cancelRequestWatcher(c *pgconn.PgConn) ctxwatch.Handler {
	return &pgconn.CancelRequestContextWatcherHandler{
		Conn:          c,
		DeadlineDelay: cancelDeadlineDelay,
	}
}

// cancelDeadlineDelay bounds how long a cancelled query may keep its connection
// before the deadline is enforced as a fallback.
const cancelDeadlineDelay = 5 * time.Second

// The row-collection helpers. Generic functions can be neither aliased nor
// bound to a variable, so unlike the types above they are one-line forwards.
// Without them the vendored pgx would keep RowToStructByName and friends
// unreachable, which is the same defect the aliases fix.
type (
	CollectableRow   = pgx.CollectableRow
	RowToFunc[T any] = pgx.RowToFunc[T]
)

var (
	RowToMap   = pgx.RowToMap
	ForEachRow = pgx.ForEachRow
)

func AppendRows[T any, S ~[]T](slice S, rows Rows, fn RowToFunc[T]) (S, error) {
	return pgx.AppendRows(slice, rows, fn)
}

func CollectRows[T any](rows Rows, fn RowToFunc[T]) ([]T, error) {
	return pgx.CollectRows(rows, fn)
}

func CollectOneRow[T any](rows Rows, fn RowToFunc[T]) (T, error) {
	return pgx.CollectOneRow(rows, fn)
}

func CollectExactlyOneRow[T any](rows Rows, fn RowToFunc[T]) (T, error) {
	return pgx.CollectExactlyOneRow(rows, fn)
}

func RowTo[T any](row CollectableRow) (T, error) { return pgx.RowTo[T](row) }

func RowToAddrOf[T any](row CollectableRow) (*T, error) { return pgx.RowToAddrOf[T](row) }

func RowToStructByPos[T any](row CollectableRow) (T, error) { return pgx.RowToStructByPos[T](row) }

func RowToAddrOfStructByPos[T any](row CollectableRow) (*T, error) {
	return pgx.RowToAddrOfStructByPos[T](row)
}

func RowToStructByName[T any](row CollectableRow) (T, error) { return pgx.RowToStructByName[T](row) }

func RowToAddrOfStructByName[T any](row CollectableRow) (*T, error) {
	return pgx.RowToAddrOfStructByName[T](row)
}

func RowToStructByNameLax[T any](row CollectableRow) (T, error) {
	return pgx.RowToStructByNameLax[T](row)
}

func RowToAddrOfStructByNameLax[T any](row CollectableRow) (*T, error) {
	return pgx.RowToAddrOfStructByNameLax[T](row)
}
