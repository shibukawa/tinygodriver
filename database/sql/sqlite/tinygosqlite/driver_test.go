//go:build cgo

package tinygosqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
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

func TestRowsCancelMidIterationInterrupts(t *testing.T) {
	db := openTestDB(t, ":memory:")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The first row (x = 0) arrives immediately; every later row costs a
	// hundred million recursive steps, so the second Next call sits inside
	// sqlite3_step until the interrupt fires.
	rows, err := db.QueryContext(ctx, `WITH RECURSIVE counter(x) AS (
        VALUES(0) UNION ALL SELECT x+1 FROM counter WHERE x < 1000000000
    ) SELECT x FROM counter WHERE x % 100000000 = 0`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatalf("first row missing: %v", rows.Err())
	}
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()
	for rows.Next() {
	}
	if err := rows.Err(); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if err := db.Ping(); err != nil {
		t.Fatalf("connection was not reusable after mid-iteration interrupt: %v", err)
	}
}

func TestRowsWatcherDoesNotLeakGoroutines(t *testing.T) {
	db := openTestDB(t, ":memory:")
	if _, err := db.Exec("CREATE TABLE item(id INTEGER PRIMARY KEY, value TEXT)"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		if _, err := db.Exec("INSERT INTO item(value) VALUES(?)", fmt.Sprintf("value-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	runQuery := func(drain bool) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		rows, err := db.QueryContext(ctx, "SELECT id, value FROM item")
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for rows.Next() {
			var id int64
			var value string
			if err := rows.Scan(&id, &value); err != nil {
				t.Fatal(err)
			}
			count++
			if !drain && count == 3 {
				break
			}
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
	}
	// Warm up the pool and helper goroutines before taking the baseline.
	runQuery(true)
	before := runtime.NumGoroutine()
	for i := 0; i < 100; i++ {
		runQuery(i%2 == 0)
	}
	// database/sql's own context watchers need a moment to exit after cancel,
	// so poll with a generous tolerance instead of demanding an exact match.
	const tolerance = 10
	deadline := time.Now().Add(2 * time.Second)
	for {
		after := runtime.NumGoroutine()
		if after <= before+tolerance {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("goroutines grew from %d to %d after 100 queries", before, after)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func BenchmarkRowsCancellableContext(b *testing.B) {
	db, err := sql.Open(DriverName, ":memory:")
	if err != nil {
		b.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE item(id INTEGER PRIMARY KEY, value TEXT, created TIMESTAMP)"); err != nil {
		b.Fatal(err)
	}
	now := time.Now().UTC()
	for i := 0; i < 100; i++ {
		if _, err := db.Exec("INSERT INTO item(value, created) VALUES(?, ?)", fmt.Sprintf("value-%d", i), now); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		rows, err := db.QueryContext(ctx, "SELECT id, value, created FROM item")
		if err != nil {
			b.Fatal(err)
		}
		for rows.Next() {
			var id int64
			var value string
			var created time.Time
			if err := rows.Scan(&id, &value, &created); err != nil {
				b.Fatal(err)
			}
		}
		if err := rows.Err(); err != nil {
			b.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			b.Fatal(err)
		}
		cancel()
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
