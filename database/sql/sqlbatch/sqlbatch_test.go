package sqlbatch_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"

	"github.com/shibukawa/tinygodriver/database/sql/sqlbatch"
)

// These cases use stub drivers rather than a real database, because what is
// under test is the registry and the refusal path, neither of which involves a
// server.

func TestUnregisteredDriverIsRefused(t *testing.T) {
	conn := &stubConn{}
	db := sql.OpenDB(stubConnector{driver: unregisteredDriver{}, conn: conn})
	defer db.Close()

	b := &sqlbatch.Batch{}
	b.Queue("INSERT INTO t(a) VALUES (1)")
	b.Queue("INSERT INTO t(a) VALUES (2)")

	err := sqlbatch.Send(context.Background(), db, b)
	var ue *sqlbatch.UnsupportedError
	if !errors.As(err, &ue) {
		t.Fatalf("error %v is not an *UnsupportedError", err)
	}
	if ue.Capability != "batch" {
		t.Errorf("Capability = %q, want %q", ue.Capability, "batch")
	}
	if !strings.Contains(ue.Driver, "unregisteredDriver") {
		t.Errorf("Driver = %q, want it to name the driver type", ue.Driver)
	}

	// The refusal must happen before anything reaches the server, so there is
	// no partial effect for the caller to undo.
	if conn.execs != 0 {
		t.Errorf("the connection ran %d statements; an unsupported batch must run none", conn.execs)
	}
}

func TestRegisteredDriverReachesItsAdapter(t *testing.T) {
	var got *sqlbatch.Batch
	sqlbatch.Register(registeredDriver{}, func(_ context.Context, _ any, b *sqlbatch.Batch, o sqlbatch.Options) (sqlbatch.Results, error) {
		got = b
		if !o.Transaction {
			t.Error("Transaction should default to true")
		}
		return &countingResults{n: b.Len()}, nil
	})

	db := sql.OpenDB(stubConnector{driver: registeredDriver{}, conn: &stubConn{}})
	defer db.Close()

	b := &sqlbatch.Batch{}
	b.Queue("SELECT 1")
	if err := sqlbatch.Send(context.Background(), db, b); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got != b {
		t.Fatal("the adapter did not receive the batch")
	}
}

func TestUnsupportedErrorMessageNamesTheFix(t *testing.T) {
	err := &sqlbatch.UnsupportedError{Driver: "d", Capability: "c", Hint: "do x"}
	if msg := err.Error(); !strings.Contains(msg, "d") || !strings.Contains(msg, "c") || !strings.Contains(msg, "do x") {
		t.Errorf("Error() = %q, want it to carry all three fields", msg)
	}
}

// StatementError with an unknown index must not read as statement 0.
func TestStatementErrorUnknownIndex(t *testing.T) {
	err := &sqlbatch.StatementError{Index: -1, Err: errors.New("boom")}
	if msg := err.Error(); !strings.Contains(msg, "unidentified") {
		t.Errorf("Error() = %q, want it to say the statement is unidentified", msg)
	}
	if !errors.Is(err, err.Err) {
		t.Error("StatementError does not unwrap")
	}
}

type unregisteredDriver struct{}

func (unregisteredDriver) Open(string) (driver.Conn, error) { return nil, errors.New("unused") }

type registeredDriver struct{}

func (registeredDriver) Open(string) (driver.Conn, error) { return nil, errors.New("unused") }

type stubConnector struct {
	driver driver.Driver
	conn   *stubConn
}

func (c stubConnector) Connect(context.Context) (driver.Conn, error) { return c.conn, nil }
func (c stubConnector) Driver() driver.Driver                        { return c.driver }

type stubConn struct{ execs int }

func (c *stubConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("unused") }
func (c *stubConn) Close() error                        { return nil }
func (c *stubConn) Begin() (driver.Tx, error)           { return nil, errors.New("unused") }

func (c *stubConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	c.execs++
	return driver.RowsAffected(0), nil
}

type countingResults struct{ n int }

func (r *countingResults) Exec() (sqlbatch.CommandTag, error) { return sqlbatch.CommandTag{}, nil }
func (r *countingResults) Query() (sqlbatch.Rows, error)      { return nil, errors.New("unused") }
func (r *countingResults) QueryRow() sqlbatch.Row             { return nil }
func (r *countingResults) Close() error                       { return nil }
