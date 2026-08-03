// Package sqlbatch sends several SQL statements per network round trip through
// a database/sql handle.
//
// database/sql has no batch verb, so N statements normally cost N round trips.
// That is expensive everywhere and worst under TinyGo, where each round trip is
// a blocking native socket read. This package queues the statements, hands them
// to whichever transport the driver actually offers, and delivers the results
// in queue order.
//
//	b := &sqlbatch.Batch{}
//	b.Queue("INSERT INTO t(a) VALUES ($1)", 1)
//	b.Queue("INSERT INTO t(a) VALUES ($1)", 2)
//	if err := sqlbatch.Send(ctx, db, b); err != nil { ... }
//
// Results are read through callbacks registered on the queued statement, so
// nothing derived from the connection escapes the batch:
//
//	var n int
//	b.Queue("SELECT count(*) FROM t").QueryRow(func(r sqlbatch.Row) error {
//		return r.Scan(&n)
//	})
//
// # Semantics
//
// A batch stops at the first failing statement, and nothing it did survives.
// Later statements do not run, even independent ones. That is PostgreSQL's
// native pipeline behavior, and the other adapters reproduce it with an
// explicit transaction. WithoutTransaction gives it up deliberately.
//
// The number of round trips a batch costs depends on the driver and is allowed
// to differ. The results and errors a caller observes are not.
//
// # Drivers
//
// A driver package registers its own adapter, so importing the driver you
// already open the database with is enough.
//
//	postgres  pgxstdlib  exec and query, pipelined
//	mysql     mysql      exec only, through multiStatements
//	sqlite    sqlite     not supported
//
// The MySQL path needs multiStatements=true, and interpolateParams=true
// whenever a statement carries arguments. Both are DSN settings negotiated when
// the connection is made, so they cannot be turned on per batch.
//
// # Unsupported drivers
//
// Send never falls back to running the statements one at a time. A driver that
// cannot serve the batch makes it fail, because the alternative is worse: a
// silent fallback would report success while costing N round trips instead of
// one, and would quietly drop the all-or-nothing guarantee that a batch on a
// working driver provides. Refusing keeps the cost of a batch something a
// caller can reason about.
//
// Every refusal is an [*UnsupportedError] naming the driver, what was missing,
// and where possible what would fix it:
//
//   - No adapter registered, which today means SQLite and any third-party
//     driver. Capability is "batch". SQLite would gain nothing anyway: a local
//     file has no round trip to save.
//   - A capability the adapter lacks, such as a queued Query on MySQL.
//     Capability names it, and the batch is refused as a whole rather than
//     running the statements it could.
//   - A DSN the adapter cannot work with, such as MySQL without
//     interpolateParams. Hint names the setting to change.
//
// In all of these the batch executes nothing at all: the refusal happens before
// any statement reaches the server, so there is no partial effect to undo.
//
// Detect it with errors.As, and fall back explicitly if that suits the caller
// better than failing:
//
//	err := sqlbatch.Send(ctx, db, b)
//	var unsupported *sqlbatch.UnsupportedError
//	if errors.As(err, &unsupported) {
//		// Run them one at a time instead, accepting the round trips.
//	}
//
// Writing that fallback by hand is deliberate. It is the caller who knows
// whether N round trips are acceptable, and whether the statements need a
// transaction around them once the batch is no longer providing one.
package sqlbatch

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"reflect"
	"sync"
)

// Batch is a set of statements to send together. Queue them, then pass the
// batch to Send. A Batch may be sent once.
type Batch struct {
	queued []*QueuedQuery
}

// Queue appends a statement and returns it, so a result callback can be
// attached. Placeholder syntax is the driver's own: $1 for PostgreSQL, ? for
// MySQL. This package does not translate between them.
func (b *Batch) Queue(sql string, args ...any) *QueuedQuery {
	q := &QueuedQuery{SQL: sql, Args: args}
	b.queued = append(b.queued, q)
	return q
}

// Len reports how many statements are queued.
func (b *Batch) Len() int { return len(b.queued) }

// Queued returns the queued statements, for adapters.
func (b *Batch) Queued() []*QueuedQuery { return b.queued }

// QueuedQuery is one statement in a Batch.
type QueuedQuery struct {
	SQL  string
	Args []any

	fn   func(Results) error
	kind kind
}

type kind int

const (
	kindExec kind = iota
	kindQuery
)

// Exec registers fn to receive this statement's result. Leaving it unset is
// fine for a write-only batch: Send reports any error either way.
func (q *QueuedQuery) Exec(fn func(CommandTag) error) *QueuedQuery {
	if fn == nil {
		return q
	}
	q.kind = kindExec
	q.fn = func(r Results) error {
		ct, err := r.Exec()
		if err != nil {
			return err
		}
		return fn(ct)
	}
	return q
}

// Query registers fn to receive this statement's rows. The rows are closed
// after fn returns and must not outlive it.
func (q *QueuedQuery) Query(fn func(Rows) error) *QueuedQuery {
	if fn == nil {
		return q
	}
	q.kind = kindQuery
	q.fn = func(r Results) error {
		rows, err := r.Query()
		if err != nil {
			return err
		}
		defer rows.Close()
		if err := fn(rows); err != nil {
			return err
		}
		return rows.Err()
	}
	return q
}

// QueryRow registers fn to receive this statement's first row. As with
// database/sql, Scan reports [sql.ErrNoRows] when there is none.
func (q *QueuedQuery) QueryRow(fn func(Row) error) *QueuedQuery {
	if fn == nil {
		return q
	}
	q.kind = kindQuery
	q.fn = func(r Results) error { return fn(r.QueryRow()) }
	return q
}

// Kind reports whether the statement was queued for its rows, for adapters that
// need to know before sending.
func (q *QueuedQuery) WantsRows() bool { return q.kind == kindQuery }

// CommandTag is what one statement reports about its effect.
type CommandTag struct {
	// RowsAffected is the number of rows the statement changed. It is 0 for a
	// statement that changes nothing, including a SELECT.
	RowsAffected int64

	// LastInsertID is meaningful only when HasLastInsertID is set. PostgreSQL
	// never reports one; use a RETURNING clause instead.
	LastInsertID    int64
	HasLastInsertID bool
}

// Rows is one statement's result set. It is the portable subset of
// [sql.Rows] and pgx.Rows.
type Rows interface {
	Next() bool
	Scan(dest ...any) error
	Columns() ([]string, error)
	Err() error
	Close() error
}

// Row is the first row of a result set.
type Row interface {
	Scan(dest ...any) error
}

// Results delivers one batch's results in queue order. It is implemented by
// adapters and driven by Send; a caller reads results through the callbacks on
// [QueuedQuery] instead.
type Results interface {
	// Exec reads the next statement's result.
	Exec() (CommandTag, error)

	// Query reads the next statement's rows.
	Query() (Rows, error)

	// QueryRow reads the next statement's first row.
	QueryRow() Row

	// Close reads and discards anything unread, releasing the connection. It
	// reports the batch's error.
	Close() error
}

// Options are the settings Send resolved, passed to the adapter.
type Options struct {
	// Transaction asks the adapter to make the batch atomic. It is true unless
	// the caller passed WithoutTransaction.
	Transaction bool
}

// An Option adjusts how a batch is sent.
type Option func(*Options)

// WithoutTransaction lets the statements commit as they go, giving up the
// all-or-nothing guarantee in exchange for one less round trip on drivers that
// need an explicit transaction.
//
// It has no effect on PostgreSQL, whose pipeline is atomic by construction:
// there is no way to ask for less.
func WithoutTransaction() Option {
	return func(o *Options) { o.Transaction = false }
}

// An Adapter runs a batch on one driver's raw connection. dc is the value
// [sql.Conn.Raw] yields, valid only until the returned Results is closed.
type Adapter func(ctx context.Context, dc any, b *Batch, o Options) (Results, error)

var registry sync.Map // reflect.Type of driver.Driver -> Adapter

// Register associates an adapter with a driver, and is meant to be called from
// a driver package's init. The key is the driver's type, so every handle opened
// through that driver finds the adapter.
//
// Registering the same driver twice panics, matching [sql.Register].
func Register(drv driver.Driver, a Adapter) {
	if drv == nil || a == nil {
		panic("sqlbatch: Register needs a driver and an adapter")
	}
	t := reflect.TypeOf(drv)
	if _, loaded := registry.LoadOrStore(t, a); loaded {
		panic("sqlbatch: adapter already registered for " + t.String())
	}
}

func lookup(drv driver.Driver) (Adapter, string) {
	t := reflect.TypeOf(drv)
	name := "<nil>"
	if t != nil {
		name = t.String()
	}
	a, ok := registry.Load(t)
	if !ok {
		return nil, name
	}
	return a.(Adapter), name
}

// UnsupportedError says a driver cannot serve part of a batch. It is returned
// rather than silently falling back, so a caller never mistakes N round trips
// for one, or an unbatched read for a batched one.
type UnsupportedError struct {
	// Driver names the driver that could not serve the request.
	Driver string

	// Capability is what was missing, such as "batch" or "query".
	Capability string

	// Hint, when set, says what would make it work.
	Hint string
}

func (e *UnsupportedError) Error() string {
	msg := fmt.Sprintf("sqlbatch: driver %s does not support %s", e.Driver, e.Capability)
	if e.Hint != "" {
		msg += " (" + e.Hint + ")"
	}
	return msg
}

// StatementError identifies the queued statement a batch failed on.
//
// Index is -1 when the driver cannot attribute the failure, which happens on
// MySQL: the server reports one error for the whole batch. An index of -1 means
// the position is unknown, never that the first statement failed.
type StatementError struct {
	Index int
	SQL   string
	Err   error
}

func (e *StatementError) Error() string {
	if e.Index < 0 {
		return fmt.Sprintf("sqlbatch: batch failed at an unidentified statement: %v", e.Err)
	}
	return fmt.Sprintf("sqlbatch: statement %d (%s): %v", e.Index, e.SQL, e.Err)
}

func (e *StatementError) Unwrap() error { return e.Err }

// Send runs the batch on a pooled connection and reports the first error.
//
// The connection is leased for the duration and returned afterwards. Every
// callback registered on a queued statement runs before Send returns; a
// statement with no callback still participates, and its result is discarded.
//
// An empty batch is a no-op.
func Send(ctx context.Context, db *sql.DB, b *Batch, opts ...Option) error {
	if b == nil || b.Len() == 0 {
		return nil
	}
	adapter, name := lookup(db.Driver())
	if adapter == nil {
		return &UnsupportedError{
			Driver:     name,
			Capability: "batch",
			Hint:       "import the driver package that registers an adapter",
		}
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	return run(ctx, conn, adapter, b, opts)
}

// There is deliberately no variant taking a *sql.Conn the caller already holds.
// Adapters are keyed on the driver, and database/sql exposes the driver on
// *sql.DB but not on *sql.Conn, so such a variant would have to take both and
// trust that they match. Batches needing session affinity are a follow-up.

func run(ctx context.Context, conn *sql.Conn, adapter Adapter, b *Batch, opts []Option) error {
	o := Options{Transaction: true}
	for _, opt := range opts {
		opt(&o)
	}
	return conn.Raw(func(dc any) error {
		res, err := adapter(ctx, dc, b, o)
		if err != nil {
			return err
		}
		return drive(res, b)
	})
}

// drive walks the results in queue order, running each callback, and always
// closes. A statement with no callback still has its result read, because the
// next one cannot be reached otherwise.
func drive(res Results, b *Batch) error {
	var first error
	for i, q := range b.Queued() {
		var err error
		if q.fn != nil {
			err = q.fn(res)
		} else if q.kind == kindQuery {
			var rows Rows
			if rows, err = res.Query(); err == nil {
				err = rows.Close()
			}
		} else {
			_, err = res.Exec()
		}
		if err != nil && first == nil {
			first = attribute(err, i, q.SQL)
			break
		}
	}
	closeErr := res.Close()
	if first != nil {
		return first
	}
	if closeErr != nil {
		return closeErr
	}
	return nil
}

// attribute names the failing statement, unless the adapter already did or the
// error is not the server's verdict on this statement.
func attribute(err error, i int, sql string) error {
	var se *StatementError
	if errors.As(err, &se) {
		return err
	}
	var ue *UnsupportedError
	if errors.As(err, &ue) {
		return err
	}
	return &StatementError{Index: i, SQL: sql, Err: err}
}

// ConvertArgs turns a queued statement's arguments into driver values, for
// adapters that reach a driver.Conn directly and therefore do not get
// database/sql's own conversion.
//
// It honors the driver's [driver.NamedValueChecker] when there is one, since a
// driver may accept types the default converter rejects.
func ConvertArgs(dc any, args []any, ordinalBase int) ([]driver.NamedValue, error) {
	if len(args) == 0 {
		return nil, nil
	}
	checker, _ := dc.(driver.NamedValueChecker)
	out := make([]driver.NamedValue, 0, len(args))
	for i, a := range args {
		nv := driver.NamedValue{Ordinal: ordinalBase + i, Value: a}
		if checker != nil {
			switch err := checker.CheckNamedValue(&nv); {
			case err == nil:
				out = append(out, nv)
				continue
			case !errors.Is(err, driver.ErrSkip):
				return nil, fmt.Errorf("sqlbatch: argument %d: %w", i, err)
			}
		}
		v, err := driver.DefaultParameterConverter.ConvertValue(a)
		if err != nil {
			return nil, fmt.Errorf("sqlbatch: argument %d: %w", i, err)
		}
		nv.Value = v
		out = append(out, nv)
	}
	return out, nil
}
