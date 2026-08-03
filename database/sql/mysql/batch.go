package mysql

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"

	"github.com/shibukawa/tinygodriver/database/sql/sqlbatch"
)

func init() { sqlbatch.Register(driverInstance(), sendBatch) }

// perStatementResult is what go-sql-driver's mysql.Result adds over
// driver.Result, and the only way to see each statement's effect: database/sql
// wraps the value and hides these.
//
// It is declared structurally rather than imported, because the concrete type
// is upstream's on host Go and the fork's on TinyGo, and both satisfy this.
type perStatementResult interface {
	AllRowsAffected() []int64
	AllLastInsertIds() []int64
}

// sendBatch runs the queued statements as one comQuery.
//
// MySQL has no pipelining and no bulk execute, so multiStatements is the only
// round-trip amortization the protocol offers: the statements are joined with
// ";" and the server replies with one result per statement.
//
// The batch is not transactional on its own. START TRANSACTION rides along in
// the same round trip, and COMMIT is appended so a successful batch still costs
// one; a failed one costs a second for the ROLLBACK, because the server stops
// at the first error and never reaches the queued COMMIT.
func sendBatch(ctx context.Context, dc any, b *sqlbatch.Batch, o sqlbatch.Options) (sqlbatch.Results, error) {
	execer, ok := dc.(driver.ExecerContext)
	if !ok {
		return nil, &sqlbatch.UnsupportedError{Driver: Backend, Capability: "batch"}
	}

	queued := b.Queued()
	stmts := make([]string, 0, len(queued)+2)
	args := make([]driver.NamedValue, 0, 8)
	if o.Transaction {
		stmts = append(stmts, "START TRANSACTION")
	}
	for i, q := range queued {
		if q.WantsRows() {
			return nil, &sqlbatch.UnsupportedError{
				Driver:     Backend,
				Capability: "query in a batch",
				Hint:       "queue only statements read through Exec, or run the read separately",
			}
		}
		if strings.TrimSpace(q.SQL) == "" {
			return nil, fmt.Errorf("sqlbatch: statement %d is empty", i)
		}
		stmts = append(stmts, strings.TrimSuffix(strings.TrimSpace(q.SQL), ";"))

		converted, err := sqlbatch.ConvertArgs(dc, q.Args, len(args)+1)
		if err != nil {
			return nil, &sqlbatch.StatementError{Index: i, SQL: q.SQL, Err: err}
		}
		args = append(args, converted...)
	}
	if o.Transaction {
		stmts = append(stmts, "COMMIT")
	}

	res, err := execer.ExecContext(ctx, strings.Join(stmts, ";"), args)
	if err != nil {
		if o.Transaction {
			// The queued COMMIT was skipped with everything else after the
			// failure, so the transaction is still open on a connection that is
			// about to go back to the pool.
			if _, rbErr := execer.ExecContext(ctx, "ROLLBACK", nil); rbErr != nil {
				err = errors.Join(err, fmt.Errorf("rollback: %w", rbErr))
			}
		}
		return nil, batchError(err, len(args) > 0)
	}

	affected, insertIDs, err := split(res, o.Transaction, len(queued))
	if err != nil {
		return nil, err
	}
	return &results{affected: affected, insertIDs: insertIDs}, nil
}

// split drops the results belonging to the transaction control statements and
// checks that what is left lines up with what was queued.
func split(res driver.Result, wrapped bool, want int) (affected, insertIDs []int64, err error) {
	per, ok := res.(perStatementResult)
	if !ok {
		return nil, nil, &sqlbatch.UnsupportedError{
			Driver:     Backend,
			Capability: "per-statement results",
			Hint:       "the driver's Result does not report AllRowsAffected",
		}
	}
	affected, insertIDs = per.AllRowsAffected(), per.AllLastInsertIds()
	if wrapped {
		// One leading entry for START TRANSACTION, one trailing for COMMIT.
		if len(affected) < 2 {
			return nil, nil, fmt.Errorf("sqlbatch: mysql returned %d results for a wrapped batch", len(affected))
		}
		affected, insertIDs = affected[1:len(affected)-1], insertIDs[1:len(insertIDs)-1]
	}
	if len(affected) != want {
		// Fewer results than statements means the server stopped early without
		// reporting an error, which would silently look like success.
		return nil, nil, fmt.Errorf("sqlbatch: mysql returned %d results for %d statements", len(affected), want)
	}
	return affected, insertIDs, nil
}

// batchError translates the failures a caller is most likely to hit into
// something that names what to change.
//
// The index is never known here: MySQL reports one error for the whole
// comQuery, and on error no Result comes back to count against.
func batchError(err error, hadArgs bool) error {
	if errors.Is(err, driver.ErrSkip) {
		// The driver spends one error value on three separate causes, and
		// nothing out here distinguishes them: interpolateParams being off, an
		// argument it cannot inline, and the rendered statement exceeding
		// max_allowed_packet all arrive as ErrSkip. Naming only the first would
		// send a caller who already set it looking in the wrong place.
		hint := "the batch may exceed max_allowed_packet"
		if hadArgs {
			hint = "set interpolateParams=true in the DSN (a multi-statement batch cannot be prepared); " +
				"or the batch exceeds max_allowed_packet; or an argument has a type the driver cannot inline"
		}
		return &sqlbatch.UnsupportedError{Driver: Backend, Capability: "batch with these settings", Hint: hint}
	}
	if strings.Contains(err.Error(), "Error 1064") {
		return &sqlbatch.StatementError{
			Index: -1,
			Err:   fmt.Errorf("%w (set multiStatements=true in the DSN if the syntax is valid)", err),
		}
	}
	return &sqlbatch.StatementError{Index: -1, Err: err}
}

// results replays what the single exchange already produced. Everything has run
// by the time this exists, so nothing here can fail.
type results struct {
	affected  []int64
	insertIDs []int64
	i         int
}

func (r *results) Exec() (sqlbatch.CommandTag, error) {
	if r.i >= len(r.affected) {
		return sqlbatch.CommandTag{}, errors.New("sqlbatch: no more results in batch")
	}
	ct := sqlbatch.CommandTag{
		RowsAffected:    r.affected[r.i],
		LastInsertID:    r.insertIDs[r.i],
		HasLastInsertID: true,
	}
	r.i++
	return ct, nil
}

func (r *results) Query() (sqlbatch.Rows, error) {
	return nil, &sqlbatch.UnsupportedError{Driver: Backend, Capability: "query in a batch"}
}

func (r *results) QueryRow() sqlbatch.Row { return errRow{} }

func (r *results) Close() error { return nil }

type errRow struct{}

func (errRow) Scan(...any) error {
	return &sqlbatch.UnsupportedError{Driver: Backend, Capability: "query in a batch"}
}
