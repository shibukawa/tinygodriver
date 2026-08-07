//go:build !tinygo

package stdlib

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"testing"
	"time"
)

// The same suite runs on both backends. Set PGX_TEST_DSN to point at a
// PostgreSQL instance; without it the tests skip, so a checkout with no
// database still passes.
//
//	docker run -d --name pgtest -e POSTGRES_PASSWORD=pass -e POSTGRES_USER=user \
//	    -e POSTGRES_DB=db -p 55432:5432 postgres:17
//	PGX_TEST_DSN='postgres://user:pass@localhost:55432/db?sslmode=disable' go test ./database/pgx/stdlib/
func testDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("PGX_TEST_DSN")
	if dsn == "" {
		t.Skip("set PGX_TEST_DSN to run stdlib integration tests")
	}
	return dsn
}

func openTest(t *testing.T) *sql.DB {
	t.Helper()
	db, err := Open(testDSN(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestOpenAndPing(t *testing.T) {
	db := openTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("Ping on %s backend: %v", backendName, err)
	}
}

func TestOpenRejectsBadDSN(t *testing.T) {
	if _, err := Open("://nonsense"); err == nil {
		t.Fatal("expected a parse error for a malformed DSN")
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
// connecting in plaintext.
func TestSSLModeUnsupportedOnVendored(t *testing.T) {
	dsn := withSSLMode(t, testDSN(t), "require")
	db, err := Open(dsn)
	if err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		err = db.PingContext(ctx)
		db.Close()
	}
	if backendName == "vendored" {
		if err == nil {
			t.Fatal("sslmode=require must not succeed on the vendored backend")
		}
		return
	}
	// Upstream pgx can do TLS; the local test server may or may not offer it,
	// so only the vendored expectation is asserted here.
	t.Logf("upstream backend, sslmode=require gave: %v", err)
}

func TestQueryTypesAndNull(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()

	var (
		i64 int64
		f64 float64
		s   string
		b   bool
		by  []byte
		ts  time.Time
	)
	err := db.QueryRowContext(ctx,
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

	var ns sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT NULL::text`).Scan(&ns); err != nil {
		t.Fatalf("scan null: %v", err)
	}
	if ns.Valid {
		t.Fatal("NULL scanned as valid")
	}
}

func TestParametersAndNoRows(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()

	var out string
	if err := db.QueryRowContext(ctx, `SELECT $1::text || '-' || $2::text`, "a", "b").Scan(&out); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if out != "a-b" {
		t.Fatalf("out = %q, want %q", out, "a-b")
	}

	var v int
	err := db.QueryRowContext(ctx, `SELECT 1 WHERE false`).Scan(&v)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("err = %v, want sql.ErrNoRows", err)
	}
}

func TestTransactionAndPreparedStatement(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()

	if _, err := db.ExecContext(ctx,
		`DROP TABLE IF EXISTS stdlib_dbsql_test;
		 CREATE TABLE stdlib_dbsql_test(id serial primary key, name text, n int)`); err != nil {
		t.Fatalf("ddl: %v", err)
	}
	t.Cleanup(func() { db.ExecContext(context.Background(), `DROP TABLE IF EXISTS stdlib_dbsql_test`) })

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	st, err := tx.PrepareContext(ctx, `INSERT INTO stdlib_dbsql_test(name, n) VALUES($1, $2)`)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	for i := 0; i < 5; i++ {
		if _, err := st.ExecContext(ctx, "row", i); err != nil {
			t.Fatalf("stmt exec: %v", err)
		}
	}
	st.Close()
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// A rolled back transaction must leave nothing behind.
	tx2, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin2: %v", err)
	}
	if _, err := tx2.ExecContext(ctx, `INSERT INTO stdlib_dbsql_test(name, n) VALUES('gone', 99)`); err != nil {
		t.Fatalf("tx2 insert: %v", err)
	}
	if err := tx2.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM stdlib_dbsql_test`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 5 {
		t.Fatalf("count = %d, want 5", n)
	}
}

func TestColumnTypes(t *testing.T) {
	db := openTest(t)
	rows, err := db.QueryContext(context.Background(), `SELECT 1::int4 AS id, 'x'::text AS name`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	cts, err := rows.ColumnTypes()
	if err != nil {
		t.Fatalf("ColumnTypes: %v", err)
	}
	if len(cts) != 2 {
		t.Fatalf("got %d columns, want 2", len(cts))
	}
	if cts[0].DatabaseTypeName() != "INT4" || cts[1].DatabaseTypeName() != "TEXT" {
		t.Fatalf("types = %s, %s", cts[0].DatabaseTypeName(), cts[1].DatabaseTypeName())
	}
}

// The reason this package overrides pgx's default watcher. On the vendored
// backend the deadline-based default silently fails to cancel anything.
func TestContextCancellation(t *testing.T) {
	db := openTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := db.ExecContext(ctx, `SELECT pg_sleep(3)`)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("query completed in %v; cancellation did not take effect", elapsed)
	}
	if elapsed > 2500*time.Millisecond {
		t.Fatalf("cancellation took %v, want well under the 3s query", elapsed)
	}
}

func TestConcurrentQueries(t *testing.T) {
	db := openTest(t)
	db.SetMaxOpenConns(4)

	ctx := context.Background()
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		go func(i int) {
			var v int
			if err := db.QueryRowContext(ctx, `SELECT $1::int`, i).Scan(&v); err != nil {
				errs <- err
				return
			}
			if v != i {
				errs <- errors.New("wrong value returned for a concurrent query")
				return
			}
			errs <- nil
		}(i)
	}
	for i := 0; i < 20; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent query: %v", err)
		}
	}
}
