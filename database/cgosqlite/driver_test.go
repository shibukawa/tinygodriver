//go:build cgo

package cgosqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func openTestDB(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open(DriverName, dsn)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Ping(); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestCRUDPreparedAndTypes(t *testing.T) {
	db := openTestDB(t, ":memory:")
	_, err := db.Exec(`CREATE TABLE items (
        id INTEGER PRIMARY KEY, name TEXT, score REAL, payload BLOB,
        enabled INTEGER, created TIMESTAMP, optional TEXT
    )`)
	if err != nil {
		t.Fatal(err)
	}

	wantTime := time.Date(2026, 7, 17, 4, 5, 6, 700, time.FixedZone("test", 9*60*60))
	stmt, err := db.Prepare("INSERT INTO items(name, score, payload, enabled, created, optional) VALUES(?, ?, ?, ?, ?, ?)")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	res, err := stmt.Exec("petit", 1.25, []byte{0, 1, 2}, true, wantTime, nil)
	if err != nil {
		t.Fatal(err)
	}
	if id, _ := res.LastInsertId(); id != 1 {
		t.Fatalf("LastInsertId = %d, want 1", id)
	}
	if count, _ := res.RowsAffected(); count != 1 {
		t.Fatalf("RowsAffected = %d, want 1", count)
	}

	var (
		name     string
		score    float64
		payload  []byte
		enabled  bool
		created  time.Time
		optional sql.NullString
	)
	err = db.QueryRow("SELECT name, score, payload, enabled, created, optional FROM items WHERE id = ?", 1).
		Scan(&name, &score, &payload, &enabled, &created, &optional)
	if err != nil {
		t.Fatal(err)
	}
	if name != "petit" || score != 1.25 || string(payload) != string([]byte{0, 1, 2}) || !enabled || optional.Valid {
		t.Fatalf("unexpected row: %q %v %v %v %#v", name, score, payload, enabled, optional)
	}
	if !created.Equal(wantTime) {
		t.Fatalf("created = %s, want instant %s", created, wantTime)
	}
}

func TestTransactionsCommitAndRollback(t *testing.T) {
	db := openTestDB(t, ":memory:")
	if _, err := db.Exec("CREATE TABLE values_table(value TEXT)"); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("INSERT INTO values_table VALUES(?)", "committed"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	tx, err = db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("INSERT INTO values_table VALUES(?)", "rolled back"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow("SELECT count(*) FROM values_table").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}
}

func TestFilePersistenceAndNamedParameters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "database.sqlite")
	db := openTestDB(t, path)
	if _, err := db.Exec("CREATE TABLE item(id INTEGER PRIMARY KEY, name TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO item(name) VALUES(:name)", sql.Named("name", "stored")); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db = openTestDB(t, "file:"+path+"?mode=ro&immutable=1")
	var got string
	if err := db.QueryRow("SELECT name FROM item").Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != "stored" {
		t.Fatalf("name = %q, want stored", got)
	}
}

func TestContextCancellationInterruptsSQLite(t *testing.T) {
	db := openTestDB(t, ":memory:")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	var value int64
	err := db.QueryRowContext(ctx, `WITH RECURSIVE counter(x) AS (
        VALUES(0) UNION ALL SELECT x+1 FROM counter WHERE x < 100000000
    ) SELECT sum(x) FROM counter`).Scan(&value)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context deadline exceeded", err)
	}
	if err := db.Ping(); err != nil {
		t.Fatalf("connection was not reusable after interrupt: %v", err)
	}
}

func TestDSNAndSQLErrors(t *testing.T) {
	for _, dsn := range []string{
		"file:test.db?unknown=1",
		"file:test.db?mode=invalid",
		"file:test.db?busy_timeout=60001",
	} {
		db, err := sql.Open(DriverName, dsn)
		if err == nil {
			err = db.Ping()
			_ = db.Close()
		}
		if err == nil {
			t.Errorf("DSN %q was accepted", dsn)
		}
	}
	db := openTestDB(t, ":memory:")
	_, err := db.Exec("SELECT * FROM missing_table")
	var sqliteErr *Error
	if !errors.As(err, &sqliteErr) || sqliteErr.Code == 0 || sqliteErr.Message == "" {
		t.Fatalf("error = %#v, want populated *Error", err)
	}
}

func TestBusyTimeoutAndConnectionReuse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "locked.sqlite")
	first := openTestDB(t, path)
	second := openTestDB(t, "file:"+path+"?busy_timeout=10")
	if _, err := first.Exec("CREATE TABLE item(value TEXT)"); err != nil {
		t.Fatal(err)
	}
	tx, err := first.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("INSERT INTO item VALUES('first')"); err != nil {
		t.Fatal(err)
	}
	_, err = second.Exec("INSERT INTO item VALUES('blocked')")
	var sqliteErr *Error
	if !errors.As(err, &sqliteErr) || sqliteErr.Code != 5 {
		t.Fatalf("locked insert error = %#v, want SQLite busy", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := second.Exec("INSERT INTO item VALUES('second')"); err != nil {
		t.Fatalf("connection was not reusable after busy: %v", err)
	}
}

func TestVersion(t *testing.T) {
	if got := Version(); got != "3.53.3" {
		t.Fatalf("Version() = %q, want 3.53.3", got)
	}
}

func TestEmptyBlobIsNotNull(t *testing.T) {
	db := openTestDB(t, ":memory:")
	var kind string
	var length int
	if err := db.QueryRow("SELECT typeof(?), length(?)", []byte{}, []byte{}).Scan(&kind, &length); err != nil {
		t.Fatal(err)
	}
	if kind != "blob" || length != 0 {
		t.Fatalf("empty []byte became (%q, %d), want (blob, 0)", kind, length)
	}
}
