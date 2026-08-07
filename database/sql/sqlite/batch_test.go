package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/shibukawa/tinygodriver/database/sql/sqlbatch"
	"github.com/shibukawa/tinygodriver/database/sql/sqlite"
)

// SQLite is served by the sequential path, so these cases assert the same
// contract the pipelined and multi-statement adapters carry: results in queue
// order, stop at the first error, roll back together.

func batchDB(t *testing.T) (*sql.DB, context.Context) {
	t.Helper()
	if sqlite.Backend == "none" {
		t.Skip("no sqlite backend in this build")
	}
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "batch.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, "CREATE TABLE item(id INTEGER PRIMARY KEY AUTOINCREMENT, value INT NOT NULL UNIQUE)"); err != nil {
		t.Fatalf("create: %v", err)
	}
	return db, ctx
}

func TestBatchExec(t *testing.T) {
	db, ctx := batchDB(t)

	tags := make([]sqlbatch.CommandTag, 3)
	b := &sqlbatch.Batch{}
	for i := range tags {
		b.Queue("INSERT INTO item(value) VALUES (?)", i+1).
			Exec(func(ct sqlbatch.CommandTag) error { tags[i] = ct; return nil })
	}
	if err := sqlbatch.Send(ctx, db, b); err != nil {
		t.Fatalf("Send on %s: %v", sqlite.Backend, err)
	}

	// Running the statements separately is what makes per-statement results
	// available at all; a multi-statement Exec would report only the last.
	for i, ct := range tags {
		if ct.RowsAffected != 1 {
			t.Errorf("statement %d affected %d rows, want 1", i, ct.RowsAffected)
		}
		if !ct.HasLastInsertID {
			t.Errorf("statement %d reported no insert id", i)
		}
		if i > 0 && ct.LastInsertID <= tags[i-1].LastInsertID {
			t.Errorf("insert ids not increasing: %v", tags)
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
	b.Queue("SELECT count(*) FROM item").
		QueryRow(func(r sqlbatch.Row) error { return r.Scan(&single) })
	b.Queue("SELECT value FROM item ORDER BY value").
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

func TestBatchRollsBackOnError(t *testing.T) {
	db, ctx := batchDB(t)
	seed(t, db, ctx, 7)

	b := &sqlbatch.Batch{}
	b.Queue("INSERT INTO item(value) VALUES (?)", 8)
	b.Queue("INSERT INTO item(value) VALUES (?)", 7) // duplicate
	b.Queue("INSERT INTO item(value) VALUES (?)", 9)

	err := sqlbatch.Send(ctx, db, b)
	if err == nil {
		t.Fatal("expected the duplicate value to fail the batch")
	}
	var se *sqlbatch.StatementError
	if !errors.As(err, &se) {
		t.Fatalf("error %v is not a *StatementError", err)
	}
	// Executing one at a time means the index is exact here, unlike MySQL.
	if se.Index != 1 {
		t.Errorf("failure attributed to statement %d, want 1", se.Index)
	}

	if n := count(t, db, ctx); n != 1 {
		t.Fatalf("table holds %d rows, want 1: the batch did not roll back", n)
	}
}

func TestWithoutTransactionKeepsEarlierStatements(t *testing.T) {
	db, ctx := batchDB(t)
	seed(t, db, ctx, 7)

	b := &sqlbatch.Batch{}
	b.Queue("INSERT INTO item(value) VALUES (?)", 8)
	b.Queue("INSERT INTO item(value) VALUES (?)", 7) // duplicate

	if err := sqlbatch.Send(ctx, db, b, sqlbatch.WithoutTransaction()); err == nil {
		t.Fatal("expected the duplicate value to fail the batch")
	}
	if n := count(t, db, ctx); n != 2 {
		t.Fatalf("table holds %d rows, want 2 (7 and 8)", n)
	}
}

func TestBatchReleasesConnection(t *testing.T) {
	db, ctx := batchDB(t)

	b := &sqlbatch.Batch{}
	b.Queue("INSERT INTO item(value) VALUES (?)", 1)
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

// The transaction is the entire reason SQLite is registered for the sequential
// path, so assert it is actually buying something rather than trusting it.
func TestBatchAmortizesTheFsync(t *testing.T) {
	if sqlite.Backend == "none" {
		t.Skip("no sqlite backend in this build")
	}
	const n = 200
	ctx := context.Background()

	elapsed := func(batched bool) time.Duration {
		db, err := sqlite.Open(filepath.Join(t.TempDir(), "cost.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		db.SetMaxOpenConns(1)
		if _, err := db.ExecContext(ctx, "CREATE TABLE t(v INT)"); err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		if batched {
			b := &sqlbatch.Batch{}
			for i := 0; i < n; i++ {
				b.Queue("INSERT INTO t(v) VALUES (?)", i)
			}
			if err := sqlbatch.Send(ctx, db, b); err != nil {
				t.Fatal(err)
			}
		} else {
			for i := 0; i < n; i++ {
				if _, err := db.ExecContext(ctx, "INSERT INTO t(v) VALUES (?)", i); err != nil {
					t.Fatal(err)
				}
			}
		}
		return time.Since(start)
	}

	loose, batched := elapsed(false), elapsed(true)
	t.Logf("%s: %d inserts loose=%v batched=%v", sqlite.Backend, n, loose.Round(time.Millisecond), batched.Round(time.Millisecond))
	// A wide margin: this asserts the transaction exists, not a throughput
	// number, and a loaded CI machine must not turn that into a flake.
	if batched >= loose {
		t.Errorf("batched %v is not faster than loose %v; the batch is probably not in a transaction", batched, loose)
	}
}

func seed(t *testing.T, db *sql.DB, ctx context.Context, values ...int) {
	t.Helper()
	for _, v := range values {
		if _, err := db.ExecContext(ctx, "INSERT INTO item(value) VALUES (?)", v); err != nil {
			t.Fatalf("seed %d: %v", v, err)
		}
	}
}

func count(t *testing.T, db *sql.DB, ctx context.Context) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM item").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}
