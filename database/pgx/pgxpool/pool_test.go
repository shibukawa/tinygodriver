//go:build !tinygo

package pgxpool

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	pgx "github.com/shibukawa/tinygodriver/database/pgx"
)

// The same suite runs on both backends: plain `go test` exercises upstream
// pgxpool, `-tags force_tinygo_logic` the vendored copy. Set PGX_TEST_DSN to
// point at a PostgreSQL instance; without it the tests skip.
func testDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("PGX_TEST_DSN")
	if dsn == "" {
		t.Skip("set PGX_TEST_DSN to run pgxpool integration tests")
	}
	return dsn
}

func newTestPool(t *testing.T) *Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := New(ctx, testDSN(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestNewAndPing(t *testing.T) {
	pool := newTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

// The per-connection defaults must survive the pool's own config parsing,
// and the pool_* DSN parameters must keep working alongside them.
func TestParseConfigInstallsDefaultsAndPoolParams(t *testing.T) {
	cfg, err := ParseConfig("postgres://user:pass@localhost:5432/db?sslmode=disable&pool_max_conns=7")
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if cfg.ConnConfig.BuildContextWatcherHandler == nil {
		t.Fatal("BuildContextWatcherHandler not installed on ConnConfig")
	}
	if cfg.MaxConns != 7 {
		t.Fatalf("MaxConns = %d, want 7", cfg.MaxConns)
	}
}

// A pool handle is shared: many goroutines query through one Pool, which is
// exactly what a bare pgx.Conn cannot do.
func TestConcurrentQueriesThroughOnePool(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	const workers = 20
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
			t.Fatalf("concurrent query: %v", err)
		}
	}
}

// Acquire hands out a *Conn whose Conn() is the native pgx connection, so
// Batch and friends work on a pooled connection too.
func TestAcquireAndBatch(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	c, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer c.Release()

	b := &pgx.Batch{}
	b.Queue(`SELECT 10`)
	b.Queue(`SELECT $1::int`, 20)
	br := c.Conn().SendBatch(ctx, b)
	sum := 0
	for i := 0; i < 2; i++ {
		var v int
		if err := br.QueryRow().Scan(&v); err != nil {
			t.Fatalf("batch row %d: %v", i, err)
		}
		sum += v
	}
	if err := br.Close(); err != nil {
		t.Fatalf("batch close: %v", err)
	}
	if sum != 30 {
		t.Fatalf("sum = %d, want 30", sum)
	}
}

func TestTransactionThroughPool(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	if _, err := pool.Exec(ctx,
		`DROP TABLE IF EXISTS pgxpool_test;
		 CREATE TABLE pgxpool_test(n int)`); err != nil {
		t.Fatalf("ddl: %v", err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), `DROP TABLE IF EXISTS pgxpool_test`) })

	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO pgxpool_test(n) VALUES (1), (2)`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	tx2, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin2: %v", err)
	}
	if _, err := tx2.Exec(ctx, `INSERT INTO pgxpool_test(n) VALUES (99)`); err != nil {
		t.Fatalf("insert2: %v", err)
	}
	if err := tx2.Rollback(ctx); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pgxpool_test`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 2 {
		t.Fatalf("count = %d, want 2", n)
	}
}

func TestErrNoRowsThroughPool(t *testing.T) {
	pool := newTestPool(t)
	var v int
	err := pool.QueryRow(context.Background(), `SELECT 1 WHERE false`).Scan(&v)
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("err = %v, want pgx.ErrNoRows", err)
	}
}

// The reason parseConfig installs the watcher: cancellation must land via
// CancelRequest on pooled connections too.
func TestContextCancellationThroughPool(t *testing.T) {
	pool := newTestPool(t)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := pool.Exec(ctx, `SELECT pg_sleep(3)`)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("query completed in %v; cancellation did not take effect", elapsed)
	}
	if elapsed > 2500*time.Millisecond {
		t.Fatalf("cancellation took %v, want well under the 3s query", elapsed)
	}
}

func TestStatReportsAcquiredConn(t *testing.T) {
	pool := newTestPool(t)
	c, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if got := pool.Stat().AcquiredConns(); got != 1 {
		c.Release()
		t.Fatalf("AcquiredConns = %d, want 1", got)
	}
	c.Release()
}
