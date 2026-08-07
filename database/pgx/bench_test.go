//go:build !tinygo

// Benchmarks comparing the native surface against the database/sql adapter on
// the same workload, so the cost of the sql layer is measured rather than
// asserted. External test package: it imports pgxstdlib, which itself imports
// this package.
package pgx_test

import (
	"context"
	"database/sql"
	"os"
	"runtime"
	"testing"

	pgx "github.com/shibukawa/tinygodriver/database/pgx"
	"github.com/shibukawa/tinygodriver/database/sql/pgxstdlib"
)

func benchDSN(b *testing.B) string {
	b.Helper()
	dsn := os.Getenv("PGX_TEST_DSN")
	if dsn == "" {
		dsn = os.Getenv("PGXSTDLIB_TEST_DSN")
	}
	if dsn == "" {
		b.Skip("set PGX_TEST_DSN to run pgx benchmarks")
	}
	return dsn
}

// One connection, one query at a time: the per-call floor of each surface.
func BenchmarkQueryRow(b *testing.B) {
	dsn := benchDSN(b)
	ctx := context.Background()

	b.Run("native", func(b *testing.B) {
		conn, err := pgx.Connect(ctx, dsn)
		if err != nil {
			b.Fatal(err)
		}
		defer conn.Close(ctx)

		b.ReportAllocs()
		for b.Loop() {
			var v int
			if err := conn.QueryRow(ctx, `SELECT $1::int`, 42).Scan(&v); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("stdlib", func(b *testing.B) {
		db, err := pgxstdlib.Open(dsn)
		if err != nil {
			b.Fatal(err)
		}
		defer db.Close()
		db.SetMaxOpenConns(1)

		b.ReportAllocs()
		for b.Loop() {
			var v int
			if err := db.QueryRowContext(ctx, `SELECT $1::int`, 42).Scan(&v); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// GOMAXPROCS workers issuing queries at once. The native side owns one
// connection per worker and never takes a lock; the stdlib side shares a
// sql.DB pool of the same size, so every query pays the pool's mutexes.
func BenchmarkQueryRowConcurrent(b *testing.B) {
	dsn := benchDSN(b)
	ctx := context.Background()
	workers := runtime.GOMAXPROCS(0)

	b.Run("native", func(b *testing.B) {
		conns := make(chan *pgx.Conn, workers)
		for i := 0; i < workers; i++ {
			conn, err := pgx.Connect(ctx, dsn)
			if err != nil {
				b.Fatal(err)
			}
			defer conn.Close(ctx)
			conns <- conn
		}

		b.ReportAllocs()
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			conn := <-conns
			defer func() { conns <- conn }()
			for pb.Next() {
				var v int
				if err := conn.QueryRow(ctx, `SELECT $1::int`, 42).Scan(&v); err != nil {
					b.Fatal(err)
				}
			}
		})
	})

	b.Run("stdlib", func(b *testing.B) {
		db, err := pgxstdlib.Open(dsn)
		if err != nil {
			b.Fatal(err)
		}
		defer db.Close()
		db.SetMaxOpenConns(workers)
		db.SetMaxIdleConns(workers)
		// Fill the pool before timing so both sides measure queries, not dials.
		var warm sql.NullInt64
		for i := 0; i < workers; i++ {
			if err := db.QueryRowContext(ctx, `SELECT 1`).Scan(&warm); err != nil {
				b.Fatal(err)
			}
		}

		b.ReportAllocs()
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				var v int
				if err := db.QueryRowContext(ctx, `SELECT $1::int`, 42).Scan(&v); err != nil {
					b.Fatal(err)
				}
			}
		})
	})
}
