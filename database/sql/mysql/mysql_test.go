package mysql

import (
	"context"
	"os"
	"testing"
)

func TestSelectedBackend(t *testing.T) {
	if Backend != expectedBackend {
		t.Fatalf("Backend = %q, want %q", Backend, expectedBackend)
	}
}

// testDSN returns the DSN for a live server, or "" when none is configured.
// Unlike SQLite there is no in-process database to fall back on.
func testDSN() string {
	if dsn := os.Getenv("MYSQL_TEST_DSN"); dsn != "" {
		return dsn
	}
	return ""
}

// TestPortableDatabaseSQLContract exercises the same database/sql surface on
// every backend, so the two implementations stay interchangeable.
func TestPortableDatabaseSQLContract(t *testing.T) {
	dsn := testDSN()
	if dsn == "" {
		t.Skip("MYSQL_TEST_DSN not set")
	}
	db, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()
	ctx := context.Background()
	if err := db.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "DROP TABLE IF EXISTS item"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "CREATE TABLE item(id INT PRIMARY KEY AUTO_INCREMENT, value TEXT, optional TEXT)"); err != nil {
		t.Fatal(err)
	}
	defer db.ExecContext(ctx, "DROP TABLE IF EXISTS item")

	stmt, err := db.PrepareContext(ctx, "INSERT INTO item(value, optional) VALUES(?, ?)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stmt.ExecContext(ctx, "keep", nil); err != nil {
		t.Fatal(err)
	}
	_ = stmt.Close()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO item(value) VALUES(?)", "discard"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}

	var value string
	var optional any
	if err := db.QueryRowContext(ctx, "SELECT value, optional FROM item").Scan(&value, &optional); err != nil {
		t.Fatal(err)
	}
	if value != "keep" || optional != nil {
		t.Fatalf("row = (%q, %#v), want (keep, nil)", value, optional)
	}
}

// TestPooledConnectionIsReused guards the reason this fork exists on the driver
// side: connCheck needs syscall.Conn, which TinyGo's net.TCPConn does not
// provide, and without the build-tag fix every query re-dialed and
// re-authenticated.
func TestPooledConnectionIsReused(t *testing.T) {
	dsn := testDSN()
	if dsn == "" {
		t.Skip("MYSQL_TEST_DSN not set")
	}
	db, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	ctx := context.Background()
	var name string
	var before int64
	if err := db.QueryRowContext(ctx, "SHOW GLOBAL STATUS LIKE 'Connections'").Scan(&name, &before); err != nil {
		t.Fatal(err)
	}
	for range 20 {
		var n int
		if err := db.QueryRowContext(ctx, "SELECT 1").Scan(&n); err != nil {
			t.Fatal(err)
		}
	}
	var after int64
	if err := db.QueryRowContext(ctx, "SHOW GLOBAL STATUS LIKE 'Connections'").Scan(&name, &after); err != nil {
		t.Fatal(err)
	}
	if got := after - before; got != 0 {
		t.Fatalf("20 queries opened %d new server connections, want 0", got)
	}
}
