//go:build !tinygo && !force_tinygo_logic

package pgx

import (
	upstream "github.com/jackc/pgx/v5"
	upstreamconn "github.com/jackc/pgx/v5/pgconn"
)

// On this build the re-exported names must BE the upstream pgx types, not
// lookalikes: code written against database/pgx has to interoperate with any
// package that names pgx types in its own signatures.
//
// These are compile-time assertions with no test function, because that is the
// whole check. They would stop holding the moment someone turned an alias into
// a wrapper type, which would compile fine everywhere else and only surface as
// a mismatch in a caller's code.
//
// The tag excludes force_tinygo_logic as well as tinygo: under that tag this
// package binds to the vendored copy, whose types are deliberately distinct.
var (
	_ *upstream.Conn                = (*Conn)(nil)
	_ *upstream.ConnConfig          = (*ConnConfig)(nil)
	_ upstream.Tx                   = Tx(nil)
	_ upstream.TxOptions            = TxOptions{}
	_ *upstream.Batch               = (*Batch)(nil)
	_ *upstream.QueuedQuery         = (*QueuedQuery)(nil)
	_ upstream.BatchResults         = BatchResults(nil)
	_ upstream.Rows                 = Rows(nil)
	_ upstream.Row                  = Row(nil)
	_ upstream.RowScanner           = RowScanner(nil)
	_ upstream.Identifier           = Identifier(nil)
	_ upstream.CopyFromSource       = CopyFromSource(nil)
	_ *upstream.LargeObjects        = (*LargeObjects)(nil)
	_ upstream.NamedArgs            = NamedArgs(nil)
	_ upstream.StrictNamedArgs      = StrictNamedArgs(nil)
	_ upstream.QueryTracer          = QueryTracer(nil)
	_ upstreamconn.CommandTag       = CommandTag{}
	_ *upstreamconn.Notification    = (*Notification)(nil)
	_ *upstreamconn.PgError         = (*PgError)(nil)
	_ *upstreamconn.PgConn          = (*PgConn)(nil)
	_ upstreamconn.FieldDescription = FieldDescription{}
)

// The forwarded constants must keep upstream's type and value.
var (
	_ upstream.TxIsoLevel    = Serializable
	_ upstream.TxAccessMode  = ReadOnly
	_ upstream.QueryExecMode = QueryExecModeSimpleProtocol
)

// The sentinel errors must be upstream's own values so errors.Is agrees across
// package boundaries.
var (
	_ = func() struct{} {
		if ErrNoRows != upstream.ErrNoRows ||
			ErrTooManyRows != upstream.ErrTooManyRows ||
			ErrTxClosed != upstream.ErrTxClosed ||
			ErrTxCommitRollback != upstream.ErrTxCommitRollback {
			panic("sentinel error identity lost")
		}
		return struct{}{}
	}()
)

// The generic helpers are forwards, not aliases, so assert the signatures match
// what upstream expects rather than the identity of the function itself.
var (
	_ upstream.RowToFunc[int]                                     = RowToStructByName[int]
	_ upstream.RowToFunc[int]                                     = RowTo[int]
	_ func(upstream.Rows, upstream.RowToFunc[int]) ([]int, error) = CollectRows[int]
)
