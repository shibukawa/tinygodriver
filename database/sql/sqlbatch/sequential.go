package sqlbatch

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// The sequential path runs the queued statements one at a time, in one
// transaction, on the leased connection.
//
// Unlike the adapters it stays above database/sql rather than reaching a
// driver.Conn, which is what makes it driver-agnostic and cheap: *sql.Rows
// already satisfies Rows, *sql.Row already satisfies Row, and sql.Result
// already reports both affected rows and the insert id. There is no conversion
// layer, and no dependence on whether a driver accepts several statements in
// one Exec.
//
// It costs one round trip per statement, so it is the default only for a driver
// that registered for it, and otherwise needs WithFallback.

// executor is the part of *sql.Tx and *sql.Conn this file uses. Both satisfy
// it, so the transactional and non-transactional paths share one loop.
type executor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func sequential(ctx context.Context, conn *sql.Conn, b *Batch, s settings) error {
	if !s.transaction {
		return drive(&seqResults{ctx: ctx, ex: conn, qs: b.Queued()}, b)
	}

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlbatch: opening the batch transaction: %w", err)
	}
	err = drive(&seqResults{ctx: ctx, ex: tx, qs: b.Queued()}, b)
	if err != nil {
		if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
			return errors.Join(err, fmt.Errorf("sqlbatch: rollback: %w", rbErr))
		}
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlbatch: committing the batch: %w", err)
	}
	return nil
}

// seqResults executes each statement as its result is asked for, so the queue
// order that drive walks is also the execution order.
type seqResults struct {
	ctx context.Context
	ex  executor
	qs  []*QueuedQuery
	i   int
}

func (r *seqResults) next() (*QueuedQuery, error) {
	if r.i >= len(r.qs) {
		return nil, errors.New("sqlbatch: no more results in batch")
	}
	q := r.qs[r.i]
	r.i++
	return q, nil
}

func (r *seqResults) Exec() (CommandTag, error) {
	q, err := r.next()
	if err != nil {
		return CommandTag{}, err
	}
	res, err := r.ex.ExecContext(r.ctx, q.SQL, q.Args...)
	if err != nil {
		return CommandTag{}, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return CommandTag{}, err
	}
	ct := CommandTag{RowsAffected: affected}
	// A driver that has no insert id reports an error rather than a zero, and
	// PostgreSQL is one of them, so this is expected rather than a failure.
	if id, err := res.LastInsertId(); err == nil {
		ct.LastInsertID, ct.HasLastInsertID = id, true
	}
	return ct, nil
}

func (r *seqResults) Query() (Rows, error) {
	q, err := r.next()
	if err != nil {
		return nil, err
	}
	return r.ex.QueryContext(r.ctx, q.SQL, q.Args...)
}

func (r *seqResults) QueryRow() Row {
	q, err := r.next()
	if err != nil {
		return errRow{err}
	}
	return r.ex.QueryRowContext(r.ctx, q.SQL, q.Args...)
}

// Close has nothing to release. Each statement's rows are closed as they are
// read, and the transaction is settled by the caller.
func (r *seqResults) Close() error { return nil }

type errRow struct{ err error }

func (r errRow) Scan(...any) error { return r.err }
