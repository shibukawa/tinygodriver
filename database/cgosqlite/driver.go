//go:build cgo

// Package cgosqlite provides a small database/sql driver backed by the
// statically linked, official SQLite amalgamation.
package cgosqlite

/*
#include <stdlib.h>
#include "bridge.h"
*/
import "C"

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"
)

const (
	// DriverName is the database/sql registration name shared by all Petitweb
	// SQLite backends.
	DriverName = "sqlite"
	maxDSNLen  = 4096
	maxSQLLen  = 1 << 20
	maxBusyMS  = 60_000
)

// Driver is the registered database/sql driver instance.
var Driver driver.Driver = &sqliteDriver{}

func init() { sql.Register(DriverName, Driver) }

// Version returns the version of the statically linked SQLite library.
func Version() string { return C.GoString(C.pw_sqlite_libversion()) }

// Error is an SQLite error with both primary and extended result codes.
type Error struct {
	Code         int
	ExtendedCode int
	Message      string
}

func (e *Error) Error() string {
	return fmt.Sprintf("sqlite: %s (code %d, extended %d)", e.Message, e.Code, e.ExtendedCode)
}

type sqliteDriver struct{}

func (d *sqliteDriver) Open(name string) (driver.Conn, error) {
	return d.open(name)
}

func (d *sqliteDriver) OpenConnector(name string) (driver.Connector, error) {
	dsn, busy, err := parseDSN(name)
	if err != nil {
		return nil, err
	}
	return &connector{driver: d, dsn: dsn, busyMS: busy}, nil
}

func (d *sqliteDriver) open(name string) (*conn, error) {
	dsn, busy, err := parseDSN(name)
	if err != nil {
		return nil, err
	}
	return openParsed(dsn, busy)
}

type connector struct {
	driver *sqliteDriver
	dsn    string
	busyMS int
}

func (c *connector) Connect(context.Context) (driver.Conn, error) {
	return openParsed(c.dsn, c.busyMS)
}
func (c *connector) Driver() driver.Driver { return c.driver }

func openParsed(dsn string, busyMS int) (*conn, error) {
	name := C.CString(dsn)
	defer C.free(unsafe.Pointer(name))
	var db *C.pw_sqlite3
	rc := C.pw_sqlite_open(name, &db)
	if rc != C.PW_SQLITE_OK {
		err := sqliteError(db, int(rc))
		if db != nil {
			C.pw_sqlite_close(db)
		}
		return nil, err
	}
	if rc = C.pw_sqlite_busy_timeout(db, C.int(busyMS)); rc != C.PW_SQLITE_OK {
		err := sqliteError(db, int(rc))
		C.pw_sqlite_close(db)
		return nil, err
	}
	return &conn{db: db}, nil
}

func parseDSN(name string) (string, int, error) {
	if len(name) == 0 || len(name) > maxDSNLen || strings.IndexByte(name, 0) >= 0 {
		return "", 0, errors.New("sqlite: DSN is empty, too long, or contains NUL")
	}
	if !strings.HasPrefix(name, "file:") {
		if strings.Contains(name, "?") {
			return "", 0, errors.New("sqlite: URI parameters require a file: DSN")
		}
		return name, 0, nil
	}
	u, err := url.Parse(name)
	if err != nil {
		return "", 0, fmt.Errorf("sqlite: invalid DSN: %w", err)
	}
	q := u.Query()
	busy := 0
	for key, values := range q {
		if len(values) != 1 {
			return "", 0, fmt.Errorf("sqlite: DSN parameter %q must occur once", key)
		}
		value := values[0]
		switch key {
		case "mode":
			if value != "ro" && value != "rw" && value != "rwc" && value != "memory" {
				return "", 0, fmt.Errorf("sqlite: invalid mode %q", value)
			}
		case "cache":
			if value != "private" && value != "shared" {
				return "", 0, fmt.Errorf("sqlite: invalid cache %q", value)
			}
		case "immutable":
			if value != "0" && value != "1" && value != "false" && value != "true" {
				return "", 0, fmt.Errorf("sqlite: invalid immutable value %q", value)
			}
		case "busy_timeout":
			busy, err = strconv.Atoi(value)
			if err != nil || busy < 0 || busy > maxBusyMS {
				return "", 0, fmt.Errorf("sqlite: busy_timeout must be between 0 and %d milliseconds", maxBusyMS)
			}
			q.Del(key)
		default:
			return "", 0, fmt.Errorf("sqlite: unsupported DSN parameter %q", key)
		}
	}
	u.RawQuery = q.Encode()
	return u.String(), busy, nil
}

type conn struct {
	mu sync.Mutex
	db *C.pw_sqlite3
}

func (c *conn) Prepare(query string) (driver.Stmt, error) {
	return c.PrepareContext(context.Background(), query)
}

func (c *conn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	stmt, err := c.prepareLocked(ctx, query)
	if err != nil {
		return nil, err
	}
	return &statement{conn: c, stmt: stmt, numInput: int(C.pw_sqlite_bind_parameter_count(stmt))}, nil
}

func (c *conn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.db == nil {
		return nil
	}
	rc := C.pw_sqlite_close(c.db)
	if rc != C.PW_SQLITE_OK {
		return c.errLocked(int(rc))
	}
	c.db = nil
	return nil
}

func (c *conn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}

func (c *conn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if opts.Isolation != driver.IsolationLevel(0) {
		return nil, errors.New("sqlite: non-default isolation level is unsupported")
	}
	if opts.ReadOnly {
		return nil, errors.New("sqlite: read-only transactions are unsupported")
	}
	if _, err := c.ExecContext(ctx, "BEGIN", nil); err != nil {
		return nil, err
	}
	return &tx{conn: c}, nil
}

func (c *conn) Ping(ctx context.Context) error {
	_, err := c.ExecContext(ctx, "SELECT 1", nil)
	return err
}

func (c *conn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	stmt, err := c.prepareLocked(ctx, query)
	if err != nil {
		return nil, err
	}
	defer C.pw_sqlite_finalize(stmt)
	if err := c.bindLocked(stmt, args); err != nil {
		return nil, err
	}
	rc := c.stepLocked(ctx, stmt)
	if rc != int(C.PW_SQLITE_DONE) && rc != int(C.PW_SQLITE_ROW) {
		return nil, c.resultError(ctx, rc)
	}
	return result{lastID: int64(C.pw_sqlite_last_insert_rowid(c.db)), rows: int64(C.pw_sqlite_changes(c.db))}, nil
}

func (c *conn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	stmt, err := c.prepareLocked(ctx, query)
	if err != nil {
		return nil, err
	}
	if err := c.bindLocked(stmt, args); err != nil {
		C.pw_sqlite_finalize(stmt)
		return nil, err
	}
	return newRows(c, stmt, ctx, true), nil
}

func (c *conn) prepareLocked(ctx context.Context, query string) (*C.pw_sqlite3_stmt, error) {
	if c.db == nil {
		return nil, driver.ErrBadConn
	}
	if len(query) == 0 || len(query) > maxSQLLen || strings.IndexByte(query, 0) >= 0 {
		return nil, errors.New("sqlite: SQL is empty, too long, or contains NUL")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cquery := C.CString(query)
	defer C.free(unsafe.Pointer(cquery))
	var stmt *C.pw_sqlite3_stmt
	rc := C.pw_sqlite_prepare(c.db, cquery, C.int(len(query)), &stmt)
	if rc != C.PW_SQLITE_OK {
		return nil, c.errLocked(int(rc))
	}
	if stmt == nil {
		return nil, errors.New("sqlite: SQL did not contain a statement")
	}
	return stmt, nil
}

func (c *conn) bindLocked(stmt *C.pw_sqlite3_stmt, args []driver.NamedValue) error {
	if len(args) != int(C.pw_sqlite_bind_parameter_count(stmt)) {
		return fmt.Errorf("sqlite: got %d arguments, want %d", len(args), int(C.pw_sqlite_bind_parameter_count(stmt)))
	}
	for _, arg := range args {
		index := arg.Ordinal
		if arg.Name != "" {
			name := arg.Name
			if name[0] != ':' && name[0] != '@' && name[0] != '$' {
				name = ":" + name
			}
			cname := C.CString(name)
			index = int(C.pw_sqlite_bind_parameter_index(stmt, cname))
			C.free(unsafe.Pointer(cname))
			if index == 0 {
				return fmt.Errorf("sqlite: unknown named parameter %q", arg.Name)
			}
		}
		var rc C.int
		switch value := arg.Value.(type) {
		case nil:
			rc = C.pw_sqlite_bind_null(stmt, C.int(index))
		case int64:
			rc = C.pw_sqlite_bind_int64(stmt, C.int(index), C.int64_t(value))
		case float64:
			rc = C.pw_sqlite_bind_double(stmt, C.int(index), C.double(value))
		case bool:
			if value {
				rc = C.pw_sqlite_bind_int64(stmt, C.int(index), 1)
			} else {
				rc = C.pw_sqlite_bind_int64(stmt, C.int(index), 0)
			}
		case string:
			p := C.CString(value)
			rc = C.pw_sqlite_bind_text(stmt, C.int(index), p, C.int(len(value)))
			C.free(unsafe.Pointer(p))
		case []byte:
			p := C.CBytes(value)
			rc = C.pw_sqlite_bind_blob(stmt, C.int(index), p, C.int(len(value)))
			C.free(p)
		case time.Time:
			text := value.UTC().Format(time.RFC3339Nano)
			p := C.CString(text)
			rc = C.pw_sqlite_bind_text(stmt, C.int(index), p, C.int(len(text)))
			C.free(unsafe.Pointer(p))
		default:
			return fmt.Errorf("sqlite: unsupported argument type %T", arg.Value)
		}
		if rc != C.PW_SQLITE_OK {
			return c.errLocked(int(rc))
		}
	}
	return nil
}

func (c *conn) stepLocked(ctx context.Context, stmt *C.pw_sqlite3_stmt) int {
	if err := ctx.Err(); err != nil {
		return int(C.PW_SQLITE_INTERRUPT)
	}
	if ctx.Done() == nil {
		return int(C.pw_sqlite_step(stmt))
	}
	finished := make(chan struct{})
	stopped := make(chan struct{})
	go func(db *C.pw_sqlite3) {
		select {
		case <-ctx.Done():
			C.pw_sqlite_interrupt(db)
		case <-finished:
		}
		close(stopped)
	}(c.db)
	rc := int(C.pw_sqlite_step(stmt))
	close(finished)
	<-stopped
	return rc
}

func (c *conn) resultError(ctx context.Context, rc int) error {
	if rc == int(C.PW_SQLITE_INTERRUPT) && ctx.Err() != nil {
		return ctx.Err()
	}
	return c.errLocked(rc)
}

func (c *conn) errLocked(rc int) error { return sqliteError(c.db, rc) }

func sqliteError(db *C.pw_sqlite3, rc int) error {
	message := "unknown error"
	extended := rc
	if db != nil {
		message = C.GoString(C.pw_sqlite_errmsg(db))
		extended = int(C.pw_sqlite_extended_errcode(db))
	}
	return &Error{Code: extended & 0xff, ExtendedCode: extended, Message: message}
}

type statement struct {
	conn     *conn
	stmt     *C.pw_sqlite3_stmt
	numInput int
}

func (s *statement) Close() error {
	s.conn.mu.Lock()
	defer s.conn.mu.Unlock()
	if s.stmt == nil {
		return nil
	}
	rc := C.pw_sqlite_finalize(s.stmt)
	s.stmt = nil
	if rc != C.PW_SQLITE_OK {
		return s.conn.errLocked(int(rc))
	}
	return nil
}
func (s *statement) NumInput() int { return s.numInput }
func (s *statement) Exec(args []driver.Value) (driver.Result, error) {
	return s.ExecContext(context.Background(), namedValues(args))
}
func (s *statement) Query(args []driver.Value) (driver.Rows, error) {
	return s.QueryContext(context.Background(), namedValues(args))
}
func (s *statement) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	s.conn.mu.Lock()
	defer s.conn.mu.Unlock()
	if s.stmt == nil || s.conn.db == nil {
		return nil, driver.ErrBadConn
	}
	C.pw_sqlite_reset(s.stmt)
	C.pw_sqlite_clear_bindings(s.stmt)
	if err := s.conn.bindLocked(s.stmt, args); err != nil {
		return nil, err
	}
	rc := s.conn.stepLocked(ctx, s.stmt)
	if rc != int(C.PW_SQLITE_DONE) && rc != int(C.PW_SQLITE_ROW) {
		return nil, s.conn.resultError(ctx, rc)
	}
	return result{lastID: int64(C.pw_sqlite_last_insert_rowid(s.conn.db)), rows: int64(C.pw_sqlite_changes(s.conn.db))}, nil
}
func (s *statement) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	s.conn.mu.Lock()
	defer s.conn.mu.Unlock()
	if s.stmt == nil || s.conn.db == nil {
		return nil, driver.ErrBadConn
	}
	C.pw_sqlite_reset(s.stmt)
	C.pw_sqlite_clear_bindings(s.stmt)
	if err := s.conn.bindLocked(s.stmt, args); err != nil {
		return nil, err
	}
	return newRows(s.conn, s.stmt, ctx, false), nil
}

func namedValues(values []driver.Value) []driver.NamedValue {
	result := make([]driver.NamedValue, len(values))
	for i, value := range values {
		result[i] = driver.NamedValue{Ordinal: i + 1, Value: value}
	}
	return result
}

type rows struct {
	conn    *conn
	stmt    *C.pw_sqlite3_stmt
	ctx     context.Context
	columns []string
	decl    []string
	owned   bool
	closed  bool
}

func newRows(c *conn, stmt *C.pw_sqlite3_stmt, ctx context.Context, owned bool) *rows {
	count := int(C.pw_sqlite_column_count(stmt))
	r := &rows{conn: c, stmt: stmt, ctx: ctx, owned: owned, columns: make([]string, count), decl: make([]string, count)}
	for i := 0; i < count; i++ {
		r.columns[i] = C.GoString(C.pw_sqlite_column_name(stmt, C.int(i)))
		decl := C.pw_sqlite_column_decltype(stmt, C.int(i))
		if decl != nil {
			r.decl[i] = strings.ToUpper(C.GoString(decl))
		}
	}
	return r
}

func (r *rows) Columns() []string { return append([]string(nil), r.columns...) }
func (r *rows) Close() error {
	r.conn.mu.Lock()
	defer r.conn.mu.Unlock()
	return r.closeLocked()
}
func (r *rows) closeLocked() error {
	if r.closed {
		return nil
	}
	r.closed = true
	if r.owned {
		rc := C.pw_sqlite_finalize(r.stmt)
		r.stmt = nil
		if rc != C.PW_SQLITE_OK {
			return r.conn.errLocked(int(rc))
		}
	} else {
		C.pw_sqlite_reset(r.stmt)
		C.pw_sqlite_clear_bindings(r.stmt)
	}
	return nil
}
func (r *rows) Next(dest []driver.Value) error {
	r.conn.mu.Lock()
	defer r.conn.mu.Unlock()
	if r.closed || r.stmt == nil {
		return io.EOF
	}
	rc := r.conn.stepLocked(r.ctx, r.stmt)
	if rc == int(C.PW_SQLITE_DONE) {
		_ = r.closeLocked()
		return io.EOF
	}
	if rc != int(C.PW_SQLITE_ROW) {
		err := r.conn.resultError(r.ctx, rc)
		_ = r.closeLocked()
		return err
	}
	for i := range dest {
		switch C.pw_sqlite_column_type(r.stmt, C.int(i)) {
		case C.PW_SQLITE_NULL:
			dest[i] = nil
		case C.PW_SQLITE_INTEGER:
			dest[i] = int64(C.pw_sqlite_column_int64(r.stmt, C.int(i)))
		case C.PW_SQLITE_FLOAT:
			dest[i] = float64(C.pw_sqlite_column_double(r.stmt, C.int(i)))
		case C.PW_SQLITE_TEXT:
			p := C.pw_sqlite_column_text(r.stmt, C.int(i))
			n := C.pw_sqlite_column_bytes(r.stmt, C.int(i))
			value := C.GoStringN((*C.char)(unsafe.Pointer(p)), n)
			if parsed, ok := parseTime(r.decl[i], value); ok {
				dest[i] = parsed
			} else {
				dest[i] = value
			}
		case C.PW_SQLITE_BLOB:
			p := C.pw_sqlite_column_blob(r.stmt, C.int(i))
			n := C.pw_sqlite_column_bytes(r.stmt, C.int(i))
			dest[i] = C.GoBytes(p, n)
		default:
			return errors.New("sqlite: unknown column type")
		}
	}
	return nil
}

func parseTime(decl, value string) (time.Time, bool) {
	if !strings.Contains(decl, "DATE") && !strings.Contains(decl, "TIME") {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999Z07:00", "2006-01-02 15:04:05", "2006-01-02"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

type tx struct {
	conn *conn
	done bool
}

func (t *tx) Commit() error   { return t.finish("COMMIT") }
func (t *tx) Rollback() error { return t.finish("ROLLBACK") }
func (t *tx) finish(command string) error {
	if t.done {
		return sql.ErrTxDone
	}
	t.done = true
	_, err := t.conn.ExecContext(context.Background(), command, nil)
	return err
}

type result struct{ lastID, rows int64 }

func (r result) LastInsertId() (int64, error) { return r.lastID, nil }
func (r result) RowsAffected() (int64, error) { return r.rows, nil }

var (
	_ driver.Driver             = (*sqliteDriver)(nil)
	_ driver.DriverContext      = (*sqliteDriver)(nil)
	_ driver.Connector          = (*connector)(nil)
	_ driver.Conn               = (*conn)(nil)
	_ driver.ConnPrepareContext = (*conn)(nil)
	_ driver.ExecerContext      = (*conn)(nil)
	_ driver.QueryerContext     = (*conn)(nil)
	_ driver.ConnBeginTx        = (*conn)(nil)
	_ driver.Pinger             = (*conn)(nil)
	_ driver.StmtExecContext    = (*statement)(nil)
	_ driver.StmtQueryContext   = (*statement)(nil)
)
