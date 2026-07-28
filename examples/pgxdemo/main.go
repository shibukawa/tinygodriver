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

	// Cancellation is the part that needs the CancelRequest watcher.
	cctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = db.ExecContext(cctx, "SELECT pg_sleep(3)")
	fmt.Printf("cancel after %v: %v\n", time.Since(start).Round(10*time.Millisecond), err)
}
