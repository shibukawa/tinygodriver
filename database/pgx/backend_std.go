//go:build !tinygo && !force_tinygo_logic

package pgx

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgconn/ctxwatch"
)

// backendName identifies the pgx in use, for tests and diagnostics.
const backendName = "upstream"

// The public surface, as aliases so every value is the upstream pgx value
// itself and satisfies upstream interfaces. The vendored backend defines the
// same names against its own copy; keeping the two sets identical is what
// makes code written against this package portable across compilers.
type (
	Conn             = pgx.Conn
	ConnConfig       = pgx.ConnConfig
	Tx               = pgx.Tx
	TxOptions        = pgx.TxOptions
	TxIsoLevel       = pgx.TxIsoLevel
	TxAccessMode     = pgx.TxAccessMode
	TxDeferrableMode = pgx.TxDeferrableMode
	Batch            = pgx.Batch
	BatchResults     = pgx.BatchResults
	QueuedQuery      = pgx.QueuedQuery
	Rows             = pgx.Rows
	Row              = pgx.Row
	RowScanner       = pgx.RowScanner
	Identifier       = pgx.Identifier
	CopyFromSource   = pgx.CopyFromSource
	LargeObjects     = pgx.LargeObjects
	NamedArgs        = pgx.NamedArgs
	StrictNamedArgs  = pgx.StrictNamedArgs
	QueryExecMode    = pgx.QueryExecMode
	QueryTracer      = pgx.QueryTracer
	CommandTag       = pgconn.CommandTag
	Notification     = pgconn.Notification
	PgError          = pgconn.PgError
	PgConn           = pgconn.PgConn
	FieldDescription = pgconn.FieldDescription
)

// Transaction characteristics, forwarded as typed constants.
const (
	Serializable    = pgx.Serializable
	RepeatableRead  = pgx.RepeatableRead
	ReadCommitted   = pgx.ReadCommitted
	ReadUncommitted = pgx.ReadUncommitted
	ReadWrite       = pgx.ReadWrite
	ReadOnly        = pgx.ReadOnly
	Deferrable      = pgx.Deferrable
	NotDeferrable   = pgx.NotDeferrable
)

// Query execution modes, for ConnConfig.DefaultQueryExecMode or as the first
// query argument.
const (
	QueryExecModeCacheStatement = pgx.QueryExecModeCacheStatement
	QueryExecModeCacheDescribe  = pgx.QueryExecModeCacheDescribe
	QueryExecModeDescribeExec   = pgx.QueryExecModeDescribeExec
	QueryExecModeExec           = pgx.QueryExecModeExec
	QueryExecModeSimpleProtocol = pgx.QueryExecModeSimpleProtocol
)

// Sentinel errors. Vars because Go cannot alias a var; errors.Is works
// unchanged since these are the same values.
var (
	ErrNoRows           = pgx.ErrNoRows
	ErrTooManyRows      = pgx.ErrTooManyRows
	ErrTxClosed         = pgx.ErrTxClosed
	ErrTxCommitRollback = pgx.ErrTxCommitRollback
)

// The CopyFrom source constructors, as variables because Go has no alias for a
// function.
var (
	CopyFromRows  = pgx.CopyFromRows
	CopyFromSlice = pgx.CopyFromSlice
	CopyFromFunc  = pgx.CopyFromFunc
)

// parseConfig parses the DSN and installs this package's defaults; see the
// package documentation.
func parseConfig(dsn string) (*ConnConfig, error) {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	cfg.BuildContextWatcherHandler = cancelRequestWatcher
	return cfg, nil
}

func connectConfig(ctx context.Context, cfg *ConnConfig) (*Conn, error) {
	return pgx.ConnectConfig(ctx, cfg)
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

// cancelDeadlineDelay bounds how long a cancelled query may keep its
// connection before the deadline is enforced as a fallback.
const cancelDeadlineDelay = 5 * time.Second

// The row-collection helpers. Generic functions can be neither aliased nor
// bound to a variable, so unlike the types above they are one-line forwards.
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
