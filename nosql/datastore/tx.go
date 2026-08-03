package datastore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// ErrTxClosed is returned by a Tx used after its closure returned.
var ErrTxClosed = errors.New("datastore: transaction is no longer usable")

// TxOption configures a transaction.
type TxOption func(*txConfig)

type txConfig struct {
	attempts int
	readOnly bool
}

// WithTxRetries caps how many times the closure re-runs on ABORTED. The default
// is 3.
func WithTxRetries(n int) TxOption {
	return func(c *txConfig) { c.attempts = n }
}

// Tx accumulates reads and mutations inside one transaction.
//
// Mutations are queued and sent with the commit, so a closure that returns an
// error writes nothing and needs no rollback on the ordinary path.
type Tx struct {
	client *Client
	handle string
	closed bool

	mutations []Mutation
}

// Get reads inside the transaction.
func (t *Tx) Get(ctx context.Context, key Key) (*Entity, error) {
	if err := t.usable(); err != nil {
		return nil, err
	}
	result, err := t.client.lookup(ctx, []Key{key}, t.handle, nil)
	if err != nil {
		return nil, err
	}
	if len(result.Found) == 0 {
		return nil, &Error{Op: "lookup", Kind: key.Kind(), StatusCode: 404,
			Status: "NOT_FOUND", Message: "no entity for key " + key.String()}
	}
	return &result.Found[0], nil
}

// GetMulti reads several entities inside the transaction.
func (t *Tx) GetMulti(ctx context.Context, keys []Key) (*LookupResult, error) {
	if err := t.usable(); err != nil {
		return nil, err
	}
	return t.client.lookup(ctx, keys, t.handle, nil)
}

// Run executes a query inside the transaction.
func (t *Tx) Run(ctx context.Context, q *Query) (*Batch, error) {
	if err := t.usable(); err != nil {
		return nil, err
	}
	return t.client.runQuery(ctx, q, t.handle, nil)
}

// Count aggregates inside the transaction.
func (t *Tx) Count(ctx context.Context, q *Query) (int64, error) {
	if err := t.usable(); err != nil {
		return 0, err
	}
	v, err := t.client.aggregate(ctx, q, t.handle, "count",
		wireAggregation{Alias: "count", Count: &wireCountAggregation{}}, nil)
	if err != nil {
		return 0, err
	}
	n, ok := v.AsInt()
	if !ok {
		return 0, fmt.Errorf("datastore: count was not an integer")
	}
	return n, nil
}

// Sum totals one property inside the transaction.
func (t *Tx) Sum(ctx context.Context, q *Query, property string) (Value, error) {
	if err := t.usable(); err != nil {
		return Value{}, err
	}
	return t.client.aggregate(ctx, q, t.handle, "sum", wireAggregation{
		Alias: "sum",
		Sum:   &wirePropertyAggregation{Property: wirePropertyReference{Name: property}},
	}, nil)
}

// Avg averages one property inside the transaction.
func (t *Tx) Avg(ctx context.Context, q *Query, property string) (Value, error) {
	if err := t.usable(); err != nil {
		return Value{}, err
	}
	return t.client.aggregate(ctx, q, t.handle, "avg", wireAggregation{
		Alias: "avg",
		Avg:   &wirePropertyAggregation{Property: wirePropertyReference{Name: property}},
	}, nil)
}

// Put queues an upsert.
func (t *Tx) Put(e Entity, opts ...WriteOption) { t.queue(UpsertOp(e).With(opts...)) }

// Insert queues an insert.
func (t *Tx) Insert(e Entity, opts ...WriteOption) { t.queue(InsertOp(e).With(opts...)) }

// Update queues an update.
func (t *Tx) Update(e Entity, opts ...WriteOption) { t.queue(UpdateOp(e).With(opts...)) }

// Delete queues a delete.
func (t *Tx) Delete(k Key, opts ...WriteOption) { t.queue(DeleteOp(k).With(opts...)) }

// Mutate queues arbitrary mutations.
func (t *Tx) Mutate(ms ...Mutation) {
	for _, m := range ms {
		t.queue(m)
	}
}

func (t *Tx) queue(m Mutation) {
	if t.closed {
		return
	}
	t.mutations = append(t.mutations, m)
}

func (t *Tx) usable() error {
	if t == nil || t.closed {
		return ErrTxClosed
	}
	return nil
}

type wireBeginTransactionRequest struct {
	DatabaseID         string          `json:"databaseId,omitempty"`
	TransactionOptions json.RawMessage `json:"transactionOptions,omitempty"`
}

type wireBeginTransactionResponse struct {
	Transaction string `json:"transaction"`
}

type wireRollbackRequest struct {
	DatabaseID  string `json:"databaseId,omitempty"`
	Transaction string `json:"transaction"`
}

// RunInTransaction runs fn inside a read-write transaction and commits what it
// queued.
//
// The closure can run more than once. Datastore reports contention as ABORTED,
// and the right response is to re-run the whole closure, not to resend the
// commit: the reads it decided on are stale. So fn must have no side effects
// outside the transaction — that cannot be enforced, only stated.
//
// This is also the only way to express a conditional write richer than the
// insert and update preconditions. The predicate runs in Go, between a read and
// a commit that share a snapshot, which is what makes it safe.
func (c *Client) RunInTransaction(ctx context.Context, fn func(*Tx) error, opts ...TxOption) error {
	cfg := txConfig{attempts: defaultAttempts}
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.attempts < 1 {
		cfg.attempts = 1
	}

	var lastErr error
	for attempt := 0; attempt < cfg.attempts; attempt++ {
		if attempt > 0 {
			if err := c.sleep(ctx, attempt); err != nil {
				return err
			}
		}
		err := c.runOneTransaction(ctx, fn, cfg)
		if err == nil {
			return nil
		}
		lastErr = err
		if !errors.Is(err, ErrAborted) {
			return err
		}
		// ABORTED means contention: run the closure again so its reads are
		// fresh.
	}
	return lastErr
}

func (c *Client) runOneTransaction(ctx context.Context, fn func(*Tx) error, cfg txConfig) (err error) {
	var begin wireBeginTransactionRequest
	begin.DatabaseID = c.database
	if cfg.readOnly {
		begin.TransactionOptions = json.RawMessage(`{"readOnly":{}}`)
	}
	var started wireBeginTransactionResponse
	if err := c.call(ctx, "beginTransaction", "", begin, &started); err != nil {
		return err
	}
	if started.Transaction == "" {
		return fmt.Errorf("datastore: beginTransaction returned no handle")
	}

	tx := &Tx{client: c, handle: started.Transaction}
	committed := false
	defer func() {
		tx.closed = true
		if committed {
			return
		}
		// Roll back on the failure path so the handle is not left holding
		// locks until it times out. A rollback that itself fails is not worth
		// reporting over the error that caused it.
		rollbackCtx := ctx
		if rollbackCtx.Err() != nil {
			rollbackCtx = context.WithoutCancel(ctx)
		}
		_ = c.call(rollbackCtx, "rollback", "",
			wireRollbackRequest{DatabaseID: c.database, Transaction: started.Transaction}, nil)
	}()

	if err := fn(tx); err != nil {
		return err
	}
	if len(tx.mutations) == 0 {
		// Nothing to write. Commit an empty transaction anyway so a read-only
		// closure still gets a consistent snapshot released cleanly.
		if _, err := c.commit(ctx, nil, started.Transaction, ""); err != nil {
			return err
		}
		committed = true
		return nil
	}
	if _, err := c.commit(ctx, tx.mutations, started.Transaction, ""); err != nil {
		return err
	}
	committed = true
	return nil
}

// RunReadOnly runs fn inside a read-only transaction, which gives several reads
// one consistent snapshot without taking write locks.
func (c *Client) RunReadOnly(ctx context.Context, fn func(*Tx) error, opts ...TxOption) error {
	all := append([]TxOption{func(c *txConfig) { c.readOnly = true }}, opts...)
	return c.RunInTransaction(ctx, func(tx *Tx) error {
		if err := fn(tx); err != nil {
			return err
		}
		if len(tx.mutations) != 0 {
			return errors.New("datastore: a read-only transaction cannot write")
		}
		return nil
	}, all...)
}
