package main

// Same source under go and tinygo. Run with:
//   go run ./examples/pgxdemo
//   tinygo build -o pgxdemo ./examples/pgxdemo && ./pgxdemo

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/shibukawa/tinygodriver/database/sql/pgxstdlib"
	"github.com/shibukawa/tinygodriver/database/sql/sqlbatch"
	_ "github.com/shibukawa/tinygodriver/netdev"
)

func main() {
	dsn := os.Getenv("PGXSTDLIB_DSN")
	if dsn == "" {
		dsn = "postgres://user:pass@localhost:55432/db?sslmode=disable"
	}

	ctx := context.Background()
	db, err := pgxstdlib.OpenContext(ctx, dsn)
	if err != nil {
		fmt.Println("open:", err)
		os.Exit(1)
	}
	defer db.Close()
	fmt.Println("connected")

	var version string
	if err := db.QueryRowContext(ctx, "SELECT version()").Scan(&version); err != nil {
		fmt.Println("query:", err)
		os.Exit(1)
	}
	if len(version) > 48 {
		version = version[:48]
	}
	fmt.Println("server:", version)

	// pg_stat_ssl describes this very session, so it reports whether the
	// connection actually ended up encrypted.
	var encrypted bool
	var tlsVersion string
	err = db.QueryRowContext(ctx,
		`SELECT ssl, coalesce(version, 'none') FROM pg_stat_ssl WHERE pid = pg_backend_pid()`).
		Scan(&encrypted, &tlsVersion)
	if err != nil {
		fmt.Println("pg_stat_ssl:", err)
		os.Exit(1)
	}
	fmt.Printf("encrypted: %v (%s)\n", encrypted, tlsVersion)

	// Batch has no database/sql equivalent, so it goes through WithConn. This
	// example is also the only place that proves the pgx types are nameable
	// from outside the package: on tinygo they live under internal/, and the
	// aliases in pgxstdlib are what make this compile.
	err = pgxstdlib.WithConn(ctx, db, func(c *pgxstdlib.Conn) error {
		b := &pgxstdlib.Batch{}
		for i := 1; i <= 3; i++ {
			b.Queue("SELECT $1::int * 10", i)
		}
		br := c.SendBatch(ctx, b)
		for i := 1; i <= 3; i++ {
			var n int
			if err := br.QueryRow().Scan(&n); err != nil {
				br.Close()
				return fmt.Errorf("batch item %d: %w", i, err)
			}
			fmt.Printf("batch %d -> %d\n", i, n)
		}
		if err := br.Close(); err != nil {
			return err
		}

		// The row-collection helpers are forwards rather than aliases, so
		// build them here too; reflection over struct tags is the part most
		// likely to break under tinygo.
		rows, err := c.Query(ctx, `SELECT 1::int4 AS id, 'a' AS name`)
		if err != nil {
			return err
		}
		type record struct {
			ID   int32
			Name string
		}
		rec, err := pgxstdlib.CollectExactlyOneRow(rows, pgxstdlib.RowToStructByName[record])
		if err != nil {
			return err
		}
		fmt.Printf("struct scan: %+v\n", rec)
		return nil
	})
	if err != nil {
		fmt.Println("batch:", err)
		os.Exit(1)
	}

	// The same batch through the portable API, which is what most callers
	// should reach for: no raw connection, no pgx types.
	var total int
	sb := &sqlbatch.Batch{}
	sb.Queue("SELECT 1")
	sb.Queue("SELECT $1::int + $2::int", 20, 22).
		QueryRow(func(r sqlbatch.Row) error { return r.Scan(&total) })
	if err := sqlbatch.Send(ctx, db, sb); err != nil {
		fmt.Println("sqlbatch:", err)
		os.Exit(1)
	}
	fmt.Println("sqlbatch total:", total)

	// Cancellation is the part that needs the CancelRequest watcher.
	cctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = db.ExecContext(cctx, "SELECT pg_sleep(3)")
	fmt.Printf("cancel after %v: %v\n", time.Since(start).Round(10*time.Millisecond), err)
}
