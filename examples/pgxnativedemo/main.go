package main

// Demonstrates the pgx-native surface of database/pgx under both compilers.
// Run with:
//   go run ./examples/pgxnativedemo
//   tinygo build -scheduler=threads -o pgxnativedemo ./examples/pgxnativedemo && ./pgxnativedemo
//
// Living outside database/pgx is this example's second job: on tinygo the
// vendored pgx sits under database/internal/, so only an external caller
// proves the re-exported surface really is reachable from user code.

import (
	"context"
	"fmt"
	"os"
	"time"

	pgx "github.com/shibukawa/tinygodriver/database/pgx"
	_ "github.com/shibukawa/tinygodriver/netdev"
)

func main() {
	dsn := os.Getenv("PGX_DSN")
	if dsn == "" {
		dsn = "postgres://user:pass@localhost:55432/db?sslmode=disable"
	}

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		fmt.Println("connect:", err)
		os.Exit(1)
	}
	defer conn.Close(ctx)
	fmt.Println("connected")

	var version string
	if err := conn.QueryRow(ctx, "SELECT version()").Scan(&version); err != nil {
		fmt.Println("query:", err)
		os.Exit(1)
	}
	if len(version) > 48 {
		version = version[:48]
	}
	fmt.Println("server:", version)

	// Batch is a first-class call here, no callback lease required.
	b := &pgx.Batch{}
	b.Queue("SELECT 10")
	b.Queue("SELECT $1::int", 20)
	b.Queue("SELECT 30")
	br := conn.SendBatch(ctx, b)
	sum := 0
	for i := 0; i < 3; i++ {
		var v int
		if err := br.QueryRow().Scan(&v); err != nil {
			fmt.Println("batch:", err)
			os.Exit(1)
		}
		sum += v
	}
	if err := br.Close(); err != nil {
		fmt.Println("batch close:", err)
		os.Exit(1)
	}
	fmt.Println("batch sum:", sum)

	// CopyFrom streams rows in binary format. Under tinygo this also
	// exercises full-duplex reads and writes on one socket.
	if _, err := conn.Exec(ctx,
		`CREATE TEMPORARY TABLE demo_copy(name text, n int)`); err != nil {
		fmt.Println("ddl:", err)
		os.Exit(1)
	}
	copied, err := conn.CopyFrom(ctx, pgx.Identifier{"demo_copy"},
		[]string{"name", "n"},
		pgx.CopyFromRows([][]any{{"a", 1}, {"b", 2}, {"c", 3}}))
	if err != nil {
		fmt.Println("copy:", err)
		os.Exit(1)
	}
	fmt.Println("copied rows:", copied)

	// The row-collection helpers run reflection over struct fields, which is
	// the part worth proving under tinygo.
	type item struct {
		Name string
		N    int
	}
	rows, err := conn.Query(ctx, `SELECT name, n FROM demo_copy ORDER BY n`)
	if err != nil {
		fmt.Println("select:", err)
		os.Exit(1)
	}
	items, err := pgx.CollectRows(rows, pgx.RowToStructByName[item])
	if err != nil {
		fmt.Println("collect:", err)
		os.Exit(1)
	}
	fmt.Println("collected:", items)

	// Cancellation must land mid-query via CancelRequest.
	cancelCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = conn.Exec(cancelCtx, "SELECT pg_sleep(3)")
	if err == nil {
		fmt.Println("cancellation did not take effect")
		os.Exit(1)
	}
	fmt.Printf("cancelled after %dms: %v\n", time.Since(start).Milliseconds(), err)

	fmt.Println("ok")
}
