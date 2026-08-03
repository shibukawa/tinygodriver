package mysql_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/shibukawa/tinygodriver/database/sql/mysql"
	"github.com/shibukawa/tinygodriver/database/sql/sqlbatch"
)

// batchDB opens a handle with the DSN settings a batch needs. Both are
// negotiated when the connection is made, so a batch cannot turn them on.
func batchDB(t *testing.T, extra string) (*sql.DB, context.Context) {
	t.Helper()
	dsn := os.Getenv("MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("set MYSQL_TEST_DSN to run mysql batch tests")
	}
	if extra != "" {
		sep := "?"
		if strings.Contains(dsn, "?") {
			sep = "&"
		}
		dsn += sep + extra
	}
	db, err := mysql.Open(dsn)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })

	ctx := context.Background()
	for _, q := range []string{
		"DROP TABLE IF EXISTS batch_item",
		"CREATE TABLE batch_item(id INT PRIMARY KEY AUTO_INCREMENT, value INT NOT NULL UNIQUE) ENGINE=InnoDB",
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	return db, ctx
}

const batchDSN = "multiStatements=true&interpolateParams=true"

func TestBatchExec(t *testing.T) {
	db, ctx := batchDB(t, batchDSN)

	tags := make([]sqlbatch.CommandTag, 3)
	b := &sqlbatch.Batch{}
	for i := range tags {
		b.Queue("INSERT INTO batch_item(value) VALUES (?)", i+1).
			Exec(func(ct sqlbatch.CommandTag) error { tags[i] = ct; return nil })
	}
	if err := sqlbatch.Send(ctx, db, b); err != nil {
		t.Fatalf("Send: %v", err)
	}

	for i, ct := range tags {
		if ct.RowsAffected != 1 {
			t.Errorf("statement %d affected %d rows, want 1", i, ct.RowsAffected)
		}
		// Unlike postgres, mysql reports an insert id per statement, and the
		// ids must be distinct or the results were misaligned.
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

// MySQL is not transactional across a multi-statement batch on its own, so the
// adapter has to supply the rollback. This is the case that would silently pass
// if it did not.
func TestBatchRollsBackOnError(t *testing.T) {
	db, ctx := batchDB(t, batchDSN)
	seed(t, db, ctx, 7)

	b := &sqlbatch.Batch{}
	b.Queue("INSERT INTO batch_item(value) VALUES (?)", 8)
	b.Queue("INSERT INTO batch_item(value) VALUES (?)", 7) // duplicate
	b.Queue("INSERT INTO batch_item(value) VALUES (?)", 9)

	err := sqlbatch.Send(ctx, db, b)
	if err == nil {
		t.Fatal("expected the duplicate value to fail the batch")
	}
	var se *sqlbatch.StatementError
	if !errors.As(err, &se) {
		t.Fatalf("error %v is not a *StatementError", err)
	}
	// The server reports one error for the whole comQuery, so the position is
	// genuinely unknown here. -1 must mean unknown, never statement 0.
	if se.Index != -1 {
		t.Errorf("Index = %d, want -1 for an unattributable mysql failure", se.Index)
	}

	if n := count(t, db, ctx); n != 1 {
		t.Fatalf("table holds %d rows, want 1: the batch did not roll back", n)
	}
}

// Without the transaction the earlier statements stand, which is what the
// option is for. Asserting it keeps the default from being mistaken for a
// property of the driver.
func TestWithoutTransactionKeepsEarlierStatements(t *testing.T) {
	db, ctx := batchDB(t, batchDSN)
	seed(t, db, ctx, 7)

	b := &sqlbatch.Batch{}
	b.Queue("INSERT INTO batch_item(value) VALUES (?)", 8)
	b.Queue("INSERT INTO batch_item(value) VALUES (?)", 7) // duplicate

	if err := sqlbatch.Send(ctx, db, b, sqlbatch.WithoutTransaction()); err == nil {
		t.Fatal("expected the duplicate value to fail the batch")
	}
	if n := count(t, db, ctx); n != 2 {
		t.Fatalf("table holds %d rows, want 2 (7 and 8)", n)
	}
}

func TestBatchQueryIsRejected(t *testing.T) {
	db, ctx := batchDB(t, batchDSN)

	b := &sqlbatch.Batch{}
	b.Queue("SELECT count(*) FROM batch_item").
		QueryRow(func(r sqlbatch.Row) error { return nil })

	err := sqlbatch.Send(ctx, db, b)
	var ue *sqlbatch.UnsupportedError
	if !errors.As(err, &ue) {
		t.Fatalf("error %v is not an *UnsupportedError", err)
	}
	if !strings.Contains(ue.Capability, "query") {
		t.Errorf("Capability = %q, want it to name query", ue.Capability)
	}
}

// The two DSN settings cannot be discovered from a driver.Conn, so the adapter
// learns about them by failing. The error has to name what to change.
func TestMissingDSNSettingsAreExplained(t *testing.T) {
	t.Run("no interpolateParams", func(t *testing.T) {
		db, ctx := batchDB(t, "multiStatements=true")
		b := &sqlbatch.Batch{}
		b.Queue("INSERT INTO batch_item(value) VALUES (?)", 1)
		b.Queue("INSERT INTO batch_item(value) VALUES (?)", 2)

		err := sqlbatch.Send(ctx, db, b)
		var ue *sqlbatch.UnsupportedError
		if !errors.As(err, &ue) {
			t.Fatalf("error %v is not an *UnsupportedError", err)
		}
		if !strings.Contains(ue.Hint, "interpolateParams") {
			t.Errorf("Hint = %q, want it to name interpolateParams", ue.Hint)
		}
	})

	t.Run("no multiStatements", func(t *testing.T) {
		db, ctx := batchDB(t, "interpolateParams=true")
		b := &sqlbatch.Batch{}
		b.Queue("INSERT INTO batch_item(value) VALUES (?)", 1)
		b.Queue("INSERT INTO batch_item(value) VALUES (?)", 2)

		err := sqlbatch.Send(ctx, db, b)
		if err == nil {
			t.Fatal("expected the server to reject a joined statement")
		}
		if !strings.Contains(err.Error(), "multiStatements") {
			t.Errorf("error %q does not mention multiStatements", err)
		}
	})
}

func TestBatchReleasesConnection(t *testing.T) {
	db, ctx := batchDB(t, batchDSN)

	b := &sqlbatch.Batch{}
	b.Queue("INSERT INTO batch_item(value) VALUES (?)", 1)
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

func seed(t *testing.T, db *sql.DB, ctx context.Context, values ...int) {
	t.Helper()
	for _, v := range values {
		if _, err := db.ExecContext(ctx, "INSERT INTO batch_item(value) VALUES (?)", v); err != nil {
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

// WithFallback turns the refusal into sequential execution. The batch costs one
// round trip per statement then, which is why it is not the default, but the
// results and the error behaviour are the ones a batched driver gives.
func TestFallbackRunsQueriesSequentially(t *testing.T) {
	db, ctx := batchDB(t, batchDSN)
	seed(t, db, ctx, 1, 2, 3)

	var total int
	var values []int
	b := &sqlbatch.Batch{}
	b.Queue("SELECT count(*) FROM batch_item").
		QueryRow(func(r sqlbatch.Row) error { return r.Scan(&total) })
	b.Queue("SELECT value FROM batch_item ORDER BY value").
		Query(func(rows sqlbatch.Rows) error {
			for rows.Next() {
				var v int
				if err := rows.Scan(&v); err != nil {
					return err
				}
				values = append(values, v)
			}
			return nil
		})

	if err := sqlbatch.Send(ctx, db, b, sqlbatch.WithFallback()); err != nil {
		t.Fatalf("Send with fallback: %v", err)
	}
	if total != 3 {
		t.Errorf("count = %d, want 3", total)
	}
	if len(values) != 3 || values[0] != 1 || values[2] != 3 {
		t.Errorf("values = %v, want [1 2 3]", values)
	}
}

// The retry after a refusal is only safe because the adapter refused before
// executing anything. If that ever stopped holding, the writes here would land
// twice.
func TestFallbackDoesNotDoubleApplyWrites(t *testing.T) {
	db, ctx := batchDB(t, batchDSN)

	b := &sqlbatch.Batch{}
	b.Queue("INSERT INTO batch_item(value) VALUES (?)", 1)
	b.Queue("INSERT INTO batch_item(value) VALUES (?)", 2)
	b.Queue("SELECT count(*) FROM batch_item").
		QueryRow(func(r sqlbatch.Row) error { return nil }) // forces the refusal

	if err := sqlbatch.Send(ctx, db, b, sqlbatch.WithFallback()); err != nil {
		t.Fatalf("Send with fallback: %v", err)
	}
	if n := count(t, db, ctx); n != 2 {
		t.Fatalf("table holds %d rows, want 2: the refused attempt also ran", n)
	}
}

// Falling back must not quietly drop atomicity either.
func TestFallbackStillRollsBack(t *testing.T) {
	db, ctx := batchDB(t, batchDSN)
	seed(t, db, ctx, 7)

	b := &sqlbatch.Batch{}
	b.Queue("INSERT INTO batch_item(value) VALUES (?)", 8)
	b.Queue("INSERT INTO batch_item(value) VALUES (?)", 7) // duplicate
	b.Queue("SELECT 1").QueryRow(func(r sqlbatch.Row) error { return nil })

	err := sqlbatch.Send(ctx, db, b, sqlbatch.WithFallback())
	if err == nil {
		t.Fatal("expected the duplicate value to fail the batch")
	}
	var se *sqlbatch.StatementError
	if !errors.As(err, &se) {
		t.Fatalf("error %v is not a *StatementError", err)
	}
	// Sequential execution knows exactly which statement failed, unlike the
	// multi-statement path.
	if se.Index != 1 {
		t.Errorf("failure attributed to statement %d, want 1", se.Index)
	}
	if n := count(t, db, ctx); n != 1 {
		t.Fatalf("table holds %d rows, want 1: the fallback did not roll back", n)
	}
}
