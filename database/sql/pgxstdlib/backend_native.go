//go:build tinygo || force_tinygo_logic

package pgxstdlib

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"net"
	"time"

	pgx "github.com/shibukawa/tinygodriver/database/internal/pgx"
	"github.com/shibukawa/tinygodriver/database/internal/pgx/pgconn"
	"github.com/shibukawa/tinygodriver/database/internal/pgx/pgconn/ctxwatch"
	"github.com/shibukawa/tinygodriver/database/internal/pgx/stdlib"
	"github.com/shibukawa/tinygodriver/https"
)

// backendName identifies the pgx in use, for tests and diagnostics.
const backendName = "vendored"

// The pgx types reachable through WithConn, same names as the std-go backend
// defines, bound here to the vendored copy.
//
// These aliases are not a convenience on this path, they are the only access
// there is: the vendored pgx sits under internal/, so no package outside
// pgxstdlib can import it, and a caller could otherwise never name the type it
// received from WithConn. See rawconn.go.
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

// open uses the vendored pgx. It differs from the standard-Go path only in
// which stdlib is imported, plus the dialer below; see internal/PATCHES.md for
// what was changed and why.
func open(dsn string) (*sql.DB, error) {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	cfg.BuildContextWatcherHandler = cancelRequestWatcher
	cfg.DialFunc = dialPlain
	return stdlib.OpenDB(*cfg), nil
}

// dialPlain replaces pgx's net.Dialer so the connection carries its descriptor.
//
// TinyGo's net.TCPConn.SyscallConn returns an error, so a descriptor cannot be
// recovered from an arbitrary net.Conn, and starting TLS on an already
// connected socket needs one. https.DialPlain returns a connection that carries
// its own, which is what makes sslmode work at all on this path.
func dialPlain(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	return https.DialPlain(ctx, host, port)
}

// cancelRequestWatcher replaces pgx's default, which cancels by moving the
// deadline on the in-flight connection. netdev reads the deadline once when a
// read begins and then blocks in select, so a later deadline change cannot
// interrupt it: the query would run to completion and return no error at all.
// Sending a CancelRequest on a second connection is what actually works here.
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
