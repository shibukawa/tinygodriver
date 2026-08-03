//go:build !tinygo

package pgxstdlib_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/shibukawa/tinygodriver/database/sql/pgxstdlib"
	"github.com/shibukawa/tinygodriver/database/sql/sqlbatch"
)

// This suite is an external test package on purpose. It exercises the batch API
// the way a caller does, from outside pgxstdlib, which is also the only vantage
// point that proves the pgx types stay nameable on the vendored backend.

func batchDB(t *testing.T) (*sql.DB, context.Context) {
	t.Helper()
	dsn := os.Getenv("PGXSTDLIB_TEST_DSN")
	if dsn == "" {
		t.Skip("set PGXSTDLIB_TEST_DSN to run pgxstdlib batch tests")
	}
	db, err := pgxstdlib.Open(dsn)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	for _, q := range []string{
		"DROP TABLE IF EXISTS batch_item",
		"CREATE TABLE batch_item(id serial PRIMARY KEY, value int NOT NULL UNIQUE)",
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	return db, ctx
}

func TestBatchExec(t *testing.T) {
	db, ctx := batchDB(t)

	tags := make([]sqlbatch.CommandTag, 3)
	b := &sqlbatch.Batch{}
	for i := range tags {
		b.Queue("INSERT INTO batch_item(value) VALUES ($1)", i+1).
			Exec(func(ct sqlbatch.CommandTag) error { tags[i] = ct; return nil })
	}
	if err := sqlbatch.Send(ctx, db, b); err != nil {
		t.Fatalf("Send: %v", err)
	}
	for i, ct := range tags {
		if ct.RowsAffected != 1 {
			t.Errorf("statement %d affected %d rows, want 1", i, ct.RowsAffected)
		}
		if ct.HasLastInsertID {
			t.Errorf("statement %d reported an insert id; postgres has none", i)
		}
	}
	if n := count(t, db, ctx); n != 3 {
		t.Fatalf("table holds %d rows, want 3", n)
	}
}

func TestBatchQuery(t *testing.T) {
	db, ctx := batchDB(t)
	seed(t, db, ctx, 1, 2, 3)

	var single int
	var all []int
	var cols []string

	b := &sqlbatch.Batch{}
	b.Queue("SELECT count(*) FROM batch_item").
		QueryRow(func(r sqlbatch.Row) error { return r.Scan(&single) })
	b.Queue("SELECT value FROM batch_item ORDER BY value").
		Query(func(rows sqlbatch.Rows) error {
			var err error
			if cols, err = rows.Columns(); err != nil {
				return err
			}
			for rows.Next() {
				var v int
				if err := rows.Scan(&v); err != nil {
					return err
				}
				all = append(all, v)
			}
			return nil
		})
	if err := sqlbatch.Send(ctx, db, b); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if single != 3 {
		t.Errorf("count = %d, want 3", single)
	}
	if len(all) != 3 || all[0] != 1 || all[2] != 3 {
		t.Errorf("values = %v, want [1 2 3]", all)
	}
	if len(cols) != 1 || cols[0] != "value" {
		t.Errorf("columns = %v, want [value]", cols)
	}
}

// A failed batch must leave nothing behind, including the statements that
// already succeeded before the failure.
func TestBatchRollsBackOnError(t *testing.T) {
	db, ctx := batchDB(t)
	seed(t, db, ctx, 7)

	b := &sqlbatch.Batch{}
	b.Queue("INSERT INTO batch_item(value) VALUES ($1)", 8)
	b.Queue("INSERT INTO batch_item(value) VALUES ($1)", 7) // duplicate, violates UNIQUE
	b.Queue("INSERT INTO batch_item(value) VALUES ($1)", 9)

	err := sqlbatch.Send(ctx, db, b)
	if err == nil {
		t.Fatal("expected the duplicate value to fail the batch")
	}

	var se *sqlbatch.StatementError
	if !errors.As(err, &se) {
		t.Fatalf("error %v is not a *StatementError", err)
	}
	if se.Index != 1 {
		t.Errorf("failure attributed to statement %d, want 1", se.Index)
	}
	if !strings.Contains(se.SQL, "INSERT") {
		t.Errorf("StatementError carries SQL %q", se.SQL)
	}

	if n := count(t, db, ctx); n != 1 {
		t.Fatalf("table holds %d rows, want 1: the batch did not roll back", n)
	}
}

// The pool must be usable immediately afterwards, so the batch cannot have left
// the connection mid-protocol.
func TestBatchReleasesConnection(t *testing.T) {
	db, ctx := batchDB(t)
	db.SetMaxOpenConns(1)

	b := &sqlbatch.Batch{}
	b.Queue("SELECT 1")
	if err := sqlbatch.Send(ctx, db, b); err != nil {
		t.Fatalf("Send: %v", err)
	}
	var n int
	if err := db.QueryRowContext(ctx, "SELECT 42").Scan(&n); err != nil {
		t.Fatalf("query after batch: %v", err)
	}
	if n != 42 {
		t.Fatalf("got %d, want 42", n)
	}
}

func TestEmptyBatchIsNoOp(t *testing.T) {
	db, ctx := batchDB(t)
	if err := sqlbatch.Send(ctx, db, &sqlbatch.Batch{}); err != nil {
		t.Fatalf("empty batch: %v", err)
	}
	if err := sqlbatch.Send(ctx, db, nil); err != nil {
		t.Fatalf("nil batch: %v", err)
	}
}

func seed(t *testing.T, db *sql.DB, ctx context.Context, values ...int) {
	t.Helper()
	for _, v := range values {
		if _, err := db.ExecContext(ctx, "INSERT INTO batch_item(value) VALUES ($1)", v); err != nil {
			t.Fatalf("seed %d: %v", v, err)
		}
	}
}

func count(t *testing.T, db *sql.DB, ctx context.Context) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM batch_item").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}
