//go:build !tinygo

package pgx

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"
)

// The same suite runs on both backends: plain `go test` exercises upstream
// pgx, `-tags force_tinygo_logic` the vendored copy. Set PGX_TEST_DSN (or
// PGXSTDLIB_TEST_DSN, so one variable drives both packages) to point at a
// PostgreSQL instance; without it the tests skip, so a checkout with no
// database still passes.
//
//	docker run -d --name pgtest -e POSTGRES_PASSWORD=pass -e POSTGRES_USER=user \
//	    -e POSTGRES_DB=db -p 55432:5432 postgres:17
//	PGX_TEST_DSN='postgres://user:pass@localhost:55432/db?sslmode=disable' go test ./database/pgx/
func testDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("PGX_TEST_DSN")
	if dsn == "" {
		dsn = os.Getenv("PGXSTDLIB_TEST_DSN")
	}
	if dsn == "" {
		t.Skip("set PGX_TEST_DSN to run pgx integration tests")
	}
	return dsn
}

func connectTest(t *testing.T) *Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := Connect(ctx, testDSN(t))
	if err != nil {
		t.Fatalf("Connect on %s backend: %v", backendName, err)
	}
	t.Cleanup(func() { conn.Close(context.Background()) })
	return conn
}

func TestConnectAndPing(t *testing.T) {
	conn := connectTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := conn.Ping(ctx); err != nil {
		t.Fatalf("Ping on %s backend: %v", backendName, err)
	}
}

func TestConnectRejectsBadDSN(t *testing.T) {
	if _, err := Connect(context.Background(), "://nonsense"); err == nil {
		t.Fatal("expected a parse error for a malformed DSN")
	}
}

// The two defaults the package documentation promises must actually be on the
// config ParseConfig returns; open-coding pgx.ParseConfig is exactly the
// mistake this package exists to prevent.
func TestParseConfigInstallsDefaults(t *testing.T) {
	cfg, err := ParseConfig("postgres://user:pass@localhost:5432/db?sslmode=disable")
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if cfg.BuildContextWatcherHandler == nil {
		t.Fatal("BuildContextWatcherHandler not installed")
	}
	if backendName == "vendored" && cfg.DialFunc == nil {
		t.Fatal("DialFunc not installed on the vendored backend")
	}
}

// withSSLMode replaces the sslmode value rather than appending one, because a
// repeated query parameter keeps its first value and would silently test the
// wrong thing.
func withSSLMode(t *testing.T, dsn, mode string) string {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse test DSN: %v", err)
	}
	q := u.Query()
	q.Set("sslmode", mode)
	u.RawQuery = q.Encode()
	return u.String()
}

// sslmode must fail loudly on the vendored backend rather than quietly
// connecting in plaintext. (The test server offers no TLS; the full TLS matrix
// lives in pgxstdlib's tls_test.go and rides the same code path.)
func TestSSLModeUnsupportedOnVendored(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := Connect(ctx, withSSLMode(t, testDSN(t), "require"))
	if err == nil {
		conn.Close(context.Background())
	}
	if backendName == "vendored" {
		if err == nil {
			t.Fatal("sslmode=require must not succeed against a plaintext-only server")
		}
		return
	}
	t.Logf("upstream backend, sslmode=require gave: %v", err)
}

func TestQueryTypesAndNull(t *testing.T) {
	conn := connectTest(t)
	ctx := context.Background()

	var (
		i64 int64
		f64 float64
		s   string
		b   bool
		by  []byte
		ts  time.Time
	)
	err := conn.QueryRow(ctx,
		`SELECT 42::int8, 1.5::float8, 'hi'::text, true, '\xdeadbeef'::bytea,
		        '2024-01-02 03:04:05+09'::timestamptz`).
		Scan(&i64, &f64, &s, &b, &by, &ts)
	if err != nil {
		t.Fatalf("scan scalars: %v", err)
	}
	if i64 != 42 || f64 != 1.5 || s != "hi" || !b {
		t.Fatalf("got %d %v %q %v", i64, f64, s, b)
	}
	if string(by) != "\xde\xad\xbe\xef" {
		t.Fatalf("bytea = %x", by)
	}

	var ns *string
	if err := conn.QueryRow(ctx, `SELECT NULL::text`).Scan(&ns); err != nil {
		t.Fatalf("scan null: %v", err)
	}
	if ns != nil {
		t.Fatal("NULL scanned as non-nil")
	}
}

func TestParametersAndErrNoRows(t *testing.T) {
	conn := connectTest(t)
	ctx := context.Background()

	var out string
	if err := conn.QueryRow(ctx, `SELECT $1::text || '-' || $2::text`, "a", "b").Scan(&out); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if out != "a-b" {
		t.Fatalf("out = %q, want %q", out, "a-b")
	}

	var v int
	err := conn.QueryRow(ctx, `SELECT 1 WHERE false`).Scan(&v)
	if !errors.Is(err, ErrNoRows) {
		t.Fatalf("err = %v, want ErrNoRows", err)
	}
}

func TestTransactionAndPreparedStatement(t *testing.T) {
	conn := connectTest(t)
	ctx := context.Background()

	if _, err := conn.Exec(ctx,
		`DROP TABLE IF EXISTS pgxnative_test;
		 CREATE TABLE pgxnative_test(id serial primary key, name text, n int)`); err != nil {
		t.Fatalf("ddl: %v", err)
	}
	t.Cleanup(func() { conn.Exec(context.Background(), `DROP TABLE IF EXISTS pgxnative_test`) })

	tx, err := conn.BeginTx(ctx, TxOptions{IsoLevel: ReadCommitted})
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.Prepare(ctx, "ins", `INSERT INTO pgxnative_test(name, n) VALUES($1, $2)`); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	for i := 0; i < 5; i++ {
		if _, err := tx.Exec(ctx, "ins", "row", i); err != nil {
			t.Fatalf("stmt exec: %v", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// A rolled back transaction must leave nothing behind, and using the
	// closed handle afterwards must report ErrTxClosed.
	tx2, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin2: %v", err)
	}
	if _, err := tx2.Exec(ctx, `INSERT INTO pgxnative_test(name, n) VALUES('gone', 99)`); err != nil {
		t.Fatalf("tx2 insert: %v", err)
	}
	if err := tx2.Rollback(ctx); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if _, err := tx2.Exec(ctx, `SELECT 1`); !errors.Is(err, ErrTxClosed) {
		t.Fatalf("after rollback err = %v, want ErrTxClosed", err)
	}

	var n int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM pgxnative_test`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 5 {
		t.Fatalf("count = %d, want 5", n)
	}
}

func TestBatch(t *testing.T) {
	conn := connectTest(t)
	ctx := context.Background()

	b := &Batch{}
	b.Queue(`SELECT 10`)
	b.Queue(`SELECT $1::int`, 20)
	b.Queue(`SELECT 30`)

	br := conn.SendBatch(ctx, b)
	var got []int
	for i := 0; i < 3; i++ {
		var v int
		if err := br.QueryRow().Scan(&v); err != nil {
			t.Fatalf("batch row %d: %v", i, err)
		}
		got = append(got, v)
	}
	if err := br.Close(); err != nil {
		t.Fatalf("batch close: %v", err)
	}
	if got[0] != 10 || got[1] != 20 || got[2] != 30 {
		t.Fatalf("batch results = %v", got)
	}
}

func TestCopyFrom(t *testing.T) {
	conn := connectTest(t)
	ctx := context.Background()

	if _, err := conn.Exec(ctx,
		`DROP TABLE IF EXISTS pgxnative_copy;
		 CREATE TABLE pgxnative_copy(name text, n int)`); err != nil {
		t.Fatalf("ddl: %v", err)
	}
	t.Cleanup(func() { conn.Exec(context.Background(), `DROP TABLE IF EXISTS pgxnative_copy`) })

	rows := [][]any{{"a", 1}, {"b", 2}, {"c", 3}}
	copied, err := conn.CopyFrom(ctx, Identifier{"pgxnative_copy"}, []string{"name", "n"}, CopyFromRows(rows))
	if err != nil {
		t.Fatalf("CopyFrom: %v", err)
	}
	if copied != 3 {
		t.Fatalf("copied = %d, want 3", copied)
	}

	var sum int
	if err := conn.QueryRow(ctx, `SELECT sum(n) FROM pgxnative_copy`).Scan(&sum); err != nil {
		t.Fatalf("sum: %v", err)
	}
	if sum != 6 {
		t.Fatalf("sum = %d, want 6", sum)
	}
}

func TestListenNotify(t *testing.T) {
	listener := connectTest(t)
	notifier := connectTest(t)
	ctx := context.Background()

	if _, err := listener.Exec(ctx, `LISTEN pgxnative_events`); err != nil {
		t.Fatalf("LISTEN: %v", err)
	}
	if _, err := notifier.Exec(ctx, `NOTIFY pgxnative_events, 'hello'`); err != nil {
		t.Fatalf("NOTIFY: %v", err)
	}

	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	n, err := listener.WaitForNotification(waitCtx)
	if err != nil {
		t.Fatalf("WaitForNotification: %v", err)
	}
	if n.Channel != "pgxnative_events" || n.Payload != "hello" {
		t.Fatalf("notification = %+v", n)
	}
}

func TestRowCollectionHelpers(t *testing.T) {
	conn := connectTest(t)
	ctx := context.Background()

	type item struct {
		Name string
		N    int
	}
	rows, err := conn.Query(ctx, `SELECT name, n FROM (VALUES ('a', 1), ('b', 2)) AS t(name, n)`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	items, err := CollectRows(rows, RowToStructByName[item])
	if err != nil {
		t.Fatalf("CollectRows: %v", err)
	}
	if len(items) != 2 || items[0] != (item{"a", 1}) || items[1] != (item{"b", 2}) {
		t.Fatalf("items = %+v", items)
	}
}

func TestFieldDescriptions(t *testing.T) {
	conn := connectTest(t)
	rows, err := conn.Query(context.Background(), `SELECT 1::int4 AS id, 'x'::text AS name`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	fds := rows.FieldDescriptions()
	if len(fds) != 2 || fds[0].Name != "id" || fds[1].Name != "name" {
		t.Fatalf("field descriptions = %+v", fds)
	}
}

func TestSQLStateViaPgError(t *testing.T) {
	conn := connectTest(t)
	_, err := conn.Exec(context.Background(), `SELECT no_such_column FROM no_such_table`)
	var pgErr *PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("err = %v, want *PgError", err)
	}
	if pgErr.Code != "42P01" { // undefined_table
		t.Fatalf("SQLSTATE = %s, want 42P01", pgErr.Code)
	}
}

// The reason ParseConfig overrides pgx's default watcher. On the vendored
// backend the deadline-based default silently fails to cancel anything.
func TestContextCancellation(t *testing.T) {
	conn := connectTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := conn.Exec(ctx, `SELECT pg_sleep(3)`)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("query completed in %v; cancellation did not take effect", elapsed)
	}
	if elapsed > 2500*time.Millisecond {
		t.Fatalf("cancellation took %v, want well under the 3s query", elapsed)
	}
}

// A native Conn is single-owner, so concurrency here means one connection per
// goroutine, not a shared handle.
func TestConcurrentConnections(t *testing.T) {
	dsn := testDSN(t)
	ctx := context.Background()

	const workers = 8
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		go func(i int) {
			conn, err := Connect(ctx, dsn)
			if err != nil {
				errs <- err
				return
			}
			defer conn.Close(ctx)
			var v int
			if err := conn.QueryRow(ctx, `SELECT $1::int`, i).Scan(&v); err != nil {
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
			t.Fatalf("concurrent connection: %v", err)
		}
	}
}
