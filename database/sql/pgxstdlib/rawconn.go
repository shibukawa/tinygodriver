package pgxstdlib

import (
	"context"
	"database/sql"
	"fmt"
)

// WithConn runs fn with the pgx connection behind one pooled database/sql
// connection, so Batch, CopyFrom and LISTEN/NOTIFY are reachable without
// leaving the database/sql surface.
//
//	err := pgxstdlib.WithConn(ctx, db, func(c *pgxstdlib.Conn) error {
//		b := &pgxstdlib.Batch{}
//		b.Queue("INSERT INTO t(a) VALUES ($1)", 1)
//		b.Queue("INSERT INTO t(a) VALUES ($1)", 2)
//		return c.SendBatch(ctx, b).Close()
//	})
//
// The connection is leased for the duration of fn and returned to the pool
// afterwards. Use WithSQLConn instead when a *sql.Conn is already held, for
// example because the work needs session state.
//
// c must not be used after fn returns, and neither may anything holding it,
// such as [BatchResults] or [Rows]. This is the contract of [sql.Conn.Raw],
// which WithConn is built on: outside fn the connection is no longer locked
// against database/sql's own use, so a read there can interleave with another
// query on the same socket. Read the results and close them inside fn.
func WithConn(ctx context.Context, db *sql.DB, fn func(*Conn) error) error {
	sc, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer sc.Close()
	return WithSQLConn(sc, fn)
}

// WithSQLConn is WithConn against a connection the caller already holds. The
// same restriction applies: nothing derived from c may outlive fn.
func WithSQLConn(sc *sql.Conn, fn func(*Conn) error) error {
	return sc.Raw(func(dc any) error {
		// The driver conn is *stdlib.Conn, whose type is one of two packages
		// depending on the build. Asking for the method instead of the type
		// keeps this file free of any build tag; the *Conn in the method
		// signature is the alias, which already resolved per build.
		unwrapper, ok := dc.(interface{ Conn() *Conn })
		if !ok {
			return fmt.Errorf("pgxstdlib: %T is not a pgx connection", dc)
		}
		return fn(unwrapper.Conn())
	})
}
