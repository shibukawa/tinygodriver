package pgxstdlib

import (
	pgx "github.com/shibukawa/tinygodriver/database/pgx"
)

// The pgx types reachable through WithConn, re-exported from database/pgx.
// That package resolves per build to upstream pgx or to the vendored copy, so
// this file needs no build tag, and a *pgxstdlib.Conn is the same type as a
// *pgx.Conn from database/pgx on either compiler.
//
// On TinyGo the re-export is not a convenience, it is the only access there
// is: the vendored pgx sits under database/internal/, which a caller outside
// this repository cannot import, so without these a callback could never name
// the type it just received. See rawconn.go.
type (
	Conn           = pgx.Conn
	Batch          = pgx.Batch
	BatchResults   = pgx.BatchResults
	QueuedQuery    = pgx.QueuedQuery
	Rows           = pgx.Rows
	Row            = pgx.Row
	Identifier     = pgx.Identifier
	CopyFromSource = pgx.CopyFromSource
	CommandTag     = pgx.CommandTag
	Notification   = pgx.Notification
	PgError        = pgx.PgError
)

// The CopyFrom source constructors, as variables because Go has no alias for a
// function.
var (
	CopyFromRows  = pgx.CopyFromRows
	CopyFromSlice = pgx.CopyFromSlice
)

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
