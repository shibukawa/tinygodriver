package stdlib

import (
	"context"
	"errors"

	pgx "github.com/shibukawa/tinygodriver/database/pgx"
	"github.com/shibukawa/tinygodriver/database/sql/sqlbatch"
)

// The adapter lives here rather than in sqlbatch because it unwraps this
// driver's connections; the pgx types come from database/pgx, which resolves
// per build.
func init() { sqlbatch.Register(driverInstance(), sendBatch) }

// sendBatch runs the queued statements as one pipelined exchange.
//
// The Options are not consulted: a pgx batch is already one implicit
// transaction that rolls back entirely on error, which is exactly the contract,
// and PostgreSQL offers no way to ask for less. WithoutTransaction is therefore
// a no-op here rather than an error, since the caller gets a stronger guarantee
// than requested, never a weaker one.
func sendBatch(ctx context.Context, dc any, b *sqlbatch.Batch, _ sqlbatch.Options) (sqlbatch.Results, error) {
	unwrapper, ok := dc.(interface{ Conn() *pgx.Conn })
	if !ok {
		return nil, &sqlbatch.UnsupportedError{
			Driver:     "pgx/stdlib",
			Capability: "batch",
			Hint:       "the connection is not a pgx connection",
		}
	}

	pb := &pgx.Batch{}
	for _, q := range b.Queued() {
		pb.Queue(q.SQL, q.Args...)
	}
	return &results{br: unwrapper.Conn().SendBatch(ctx, pb)}, nil
}

// results adapts pgx.BatchResults, which differs from the portable interface
// only in how rows report themselves.
type results struct {
	br pgx.BatchResults
}

func (r *results) Exec() (sqlbatch.CommandTag, error) {
	ct, err := r.br.Exec()
	if err != nil {
		return sqlbatch.CommandTag{}, err
	}
	// PostgreSQL has no insert id; RETURNING is how a caller gets one, so
	// HasLastInsertID stays false.
	return sqlbatch.CommandTag{RowsAffected: ct.RowsAffected()}, nil
}

func (r *results) Query() (sqlbatch.Rows, error) {
	rows, err := r.br.Query()
	if err != nil {
		return nil, err
	}
	return &pgxRows{rows}, nil
}

func (r *results) QueryRow() sqlbatch.Row { return r.br.QueryRow() }

func (r *results) Close() error { return r.br.Close() }

// pgxRows squares pgx.Rows with the portable interface: Close returns no error
// there, and column names come from the field descriptions.
type pgxRows struct{ pgx.Rows }

func (r *pgxRows) Close() error {
	r.Rows.Close()
	return r.Rows.Err()
}

func (r *pgxRows) Columns() ([]string, error) {
	fds := r.Rows.FieldDescriptions()
	if fds == nil {
		if err := r.Rows.Err(); err != nil {
			return nil, err
		}
		return nil, errors.New("pgx/stdlib: no column information")
	}
	names := make([]string, len(fds))
	for i, fd := range fds {
		names[i] = fd.Name
	}
	return names, nil
}
