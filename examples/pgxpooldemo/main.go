package main

// Demonstrates database/pgx/pgxpool under both compilers. Run with:
//   go run ./examples/pgxpooldemo
//   tinygo build -scheduler=threads -o pgxpooldemo ./examples/pgxpooldemo && ./pgxpooldemo
//
// Living outside database/ is this example's second job: on tinygo the
// vendored pgxpool sits under database/internal/, so only an external caller
// proves the re-exported surface is reachable from user code.

import (
	"context"
	"fmt"
	"os"
	"time"

	pgx "github.com/shibukawa/tinygodriver/database/pgx"
	"github.com/shibukawa/tinygodriver/database/pgx/pgxpool"
	_ "github.com/shibukawa/tinygodriver/netdev"
)

func main() {
	dsn := os.Getenv("PGX_DSN")
	if dsn == "" {
		dsn = "postgres://user:pass@localhost:55432/db?sslmode=disable&pool_max_conns=4"
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		fmt.Println("new:", err)
		os.Exit(1)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		fmt.Println("ping:", err)
		os.Exit(1)
	}
	fmt.Println("connected")

	// Many goroutines sharing one pool is the whole point of pgxpool; a bare
	// pgx.Conn cannot do this.
	const workers = 8
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		go func(i int) {
			var v int
			if err := pool.QueryRow(ctx, `SELECT $1::int`, i).Scan(&v); err != nil {
				errs <- err
				return
			}
			if v != i {
				errs <- fmt.Errorf("got %d, want %d", v, i)
				return
			}
			errs <- nil
		}(i)
	}
	for i := 0; i < workers; i++ {
		if err := <-errs; err != nil {
			fmt.Println("worker:", err)
			os.Exit(1)
		}
	}
	fmt.Printf("%d concurrent queries through %d conns\n", workers, pool.Stat().TotalConns())

	// A pooled connection still reaches the native pgx surface.
	c, err := pool.Acquire(ctx)
	if err != nil {
		fmt.Println("acquire:", err)
		os.Exit(1)
	}
	b := &pgx.Batch{}
	b.Queue("SELECT 10")
	b.Queue("SELECT 20")
	br := c.Conn().SendBatch(ctx, b)
	sum := 0
	for i := 0; i < 2; i++ {
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
	c.Release()
	fmt.Println("batch sum:", sum)

	// Cancellation must land on pooled connections too.
	cancelCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := pool.Exec(cancelCtx, "SELECT pg_sleep(3)"); err == nil {
		fmt.Println("cancellation did not take effect")
		os.Exit(1)
	}
	fmt.Printf("cancelled after %dms\n", time.Since(start).Milliseconds())

	fmt.Println("ok")
}
