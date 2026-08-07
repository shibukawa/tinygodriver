package sqlite

import (
	"context"
	"testing"
)

func TestSelectedBackend(t *testing.T) {
	if Backend != expectedBackend {
		t.Fatalf("Backend = %q, want %q", Backend, expectedBackend)
	}
}

func TestPortableDatabaseSQLContract(t *testing.T) {
	if Backend == "none" {
		t.Skip("no sqlite backend in this build")
	}
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()
	ctx := context.Background()
	if err := db.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "CREATE TABLE item(id INTEGER PRIMARY KEY, value TEXT, optional TEXT)"); err != nil {
		t.Fatal(err)
	}
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

func BenchmarkInsertAndQuery(b *testing.B) {
	db, err := Open(":memory:")
	if err != nil {
		b.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE item(id INTEGER PRIMARY KEY, value TEXT)"); err != nil {
		b.Fatal(err)
	}
	insert, err := db.Prepare("INSERT INTO item(value) VALUES(?)")
	if err != nil {
		b.Fatal(err)
	}
	defer insert.Close()
	query, err := db.Prepare("SELECT value FROM item WHERE id = ?")
	if err != nil {
		b.Fatal(err)
	}
	defer query.Close()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result, err := insert.Exec("value")
		if err != nil {
			b.Fatal(err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			b.Fatal(err)
		}
		var value string
		if err := query.QueryRow(id).Scan(&value); err != nil {
			b.Fatal(err)
		}
	}
}
