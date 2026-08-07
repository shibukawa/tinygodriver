//go:build !tinygo

package stdlib

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"

	pgx "github.com/shibukawa/tinygodriver/database/pgx"
)

// These cases run on both backends, like the rest of the suite. What they
// cannot prove from in here is the import boundary that broke the escape hatch
// in the first place: this package may import its own internal/, so a test
// living beside it compiles either way. examples/pgxdemo is the check that
// matters, because it sits outside the tree and fails to build without the
// re-exports in database/pgx.

func TestWithConnReachesPgx(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()

	var got string
	err := WithConn(ctx, db, func(c *pgx.Conn) error {
		return c.QueryRow(ctx, `SELECT 'reached'`).Scan(&got)
	})
	if err != nil {
		t.Fatalf("WithConn on %s backend: %v", backendName, err)
	}
	if got != "reached" {
		t.Fatalf("got %q, want %q", got, "reached")
	}
}

func TestWithConnBatch(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()

	var got []int
	err := WithConn(ctx, db, func(c *pgx.Conn) error {
		b := &pgx.Batch{}
		for i := 1; i <= 3; i++ {
			b.Queue(`SELECT $1::int * 10`, i)
		}
		br := c.SendBatch(ctx, b)
		for i := 0; i < 3; i++ {
			var n int
			if err := br.QueryRow().Scan(&n); err != nil {
				br.Close()
				return err
			}
			got = append(got, n)
		}
		return br.Close()
	})
	if err != nil {
		t.Fatalf("batch on %s backend: %v", backendName, err)
	}
	want := []int{10, 20, 30}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

// A failing statement must not leave the connection unusable, since WithConn
// hands it straight back to the pool.
func TestWithConnBatchErrorReleasesConn(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()

	err := WithConn(ctx, db, func(c *pgx.Conn) error {
		b := &pgx.Batch{}
		b.Queue(`SELECT 1`)
		b.Queue(`SELECT 1/0`)
		b.Queue(`SELECT 1`)
		return c.SendBatch(ctx, b).Close()
	})
	if err == nil {
		t.Fatal("expected the division by zero to fail the batch")
	}
	var pgErr *pgx.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("expected a *PgError, got %T: %v", err, err)
	}
	if pgErr.Code != "22012" { // division_by_zero
		t.Fatalf("got SQLSTATE %s, want 22012", pgErr.Code)
	}

	var n int
	if err := db.QueryRowContext(ctx, `SELECT 1`).Scan(&n); err != nil {
		t.Fatalf("pool unusable after a failed batch: %v", err)
	}
}

// The batch is one implicit transaction, so a statement after the failure is
// not merely skipped: the ones before it are rolled back too.
func TestWithConnBatchIsAtomic(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()

	// A temp table belongs to one session, so every step here runs on the same
	// *sql.Conn. Going through the pool could reach a different backend and
	// make the count meaningless.
	sc, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("Conn: %v", err)
	}
	defer sc.Close()

	if _, err := sc.ExecContext(ctx, `CREATE TEMP TABLE batch_atomic (n int)`); err != nil {
		t.Fatalf("create temp table: %v", err)
	}

	err = WithSQLConn(sc, func(c *pgx.Conn) error {
		b := &pgx.Batch{}
		b.Queue(`INSERT INTO batch_atomic VALUES (1)`)
		b.Queue(`INSERT INTO batch_atomic VALUES (1/0)`)
		return c.SendBatch(ctx, b).Close()
	})
	if err == nil {
		t.Fatal("expected the batch to fail")
	}

	var count int
	if err := sc.QueryRowContext(ctx, `SELECT count(*) FROM batch_atomic`).Scan(&count); err != nil {
		t.Fatalf("count after a failed batch: %v", err)
	}
	if count != 0 {
		t.Fatalf("got %d rows after a failed batch, want 0; the batch was not atomic", count)
	}
}

func TestWithSQLConnUsesTheGivenConn(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()

	sc, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("Conn: %v", err)
	}
	defer sc.Close()

	var viaPgx, viaSQL int
	if err := sc.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&viaSQL); err != nil {
		t.Fatalf("pg_backend_pid through database/sql: %v", err)
	}
	err = WithSQLConn(sc, func(c *pgx.Conn) error {
		return c.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&viaPgx)
	})
	if err != nil {
		t.Fatalf("WithSQLConn: %v", err)
	}
	if viaPgx != viaSQL {
		t.Fatalf("pgx saw backend pid %d, database/sql saw %d; not the same session", viaPgx, viaSQL)
	}
}

// A driver that is not pgx must be reported, not panic through a bare type
// assertion.
func TestWithSQLConnRejectsForeignDriver(t *testing.T) {
	sql.Register("stdlib_notpgx", notPgxDriver{})
	db, err := sql.Open("stdlib_notpgx", "")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	err = WithConn(context.Background(), db, func(*pgx.Conn) error {
		t.Fatal("fn must not run for a non-pgx driver")
		return nil
	})
	if err == nil {
		t.Fatal("expected an error for a non-pgx driver")
	}
}

// The row-collection helpers are forwards rather than aliases in
// database/pgx, so they need their own case: a wrong type parameter or a
// swapped argument would still compile against the alias set.
func TestRowCollectionHelpers(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()

	type row struct {
		ID   int32
		Name string
	}

	var byName []row
	var byPos []row
	var one row
	err := WithConn(ctx, db, func(c *pgx.Conn) error {
		const q = `SELECT 1::int4 AS id, 'a' AS name UNION ALL SELECT 2, 'b' ORDER BY id`

		rows, err := c.Query(ctx, q)
		if err != nil {
			return err
		}
		if byName, err = pgx.CollectRows(rows, pgx.RowToStructByName[row]); err != nil {
			return err
		}

		if rows, err = c.Query(ctx, q); err != nil {
			return err
		}
		if byPos, err = pgx.CollectRows(rows, pgx.RowToStructByPos[row]); err != nil {
			return err
		}

		if rows, err = c.Query(ctx, `SELECT 7::int4, 'seven'`); err != nil {
			return err
		}
		one, err = pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[row])
		return err
	})
	if err != nil {
		t.Fatalf("row helpers on %s backend: %v", backendName, err)
	}

	want := []row{{1, "a"}, {2, "b"}}
	for _, got := range [][]row{byName, byPos} {
		if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
	if one != (row{7, "seven"}) {
		t.Fatalf("got %v, want {7 seven}", one)
	}
}

type notPgxDriver struct{}

func (notPgxDriver) Open(string) (driver.Conn, error) { return notPgxConn{}, nil }

type notPgxConn struct{}

func (notPgxConn) Prepare(string) (driver.Stmt, error) { return nil, errors.ErrUnsupported }
func (notPgxConn) Close() error                        { return nil }
func (notPgxConn) Begin() (driver.Tx, error)           { return nil, errors.ErrUnsupported }
