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
//
// The transaction starts lazily. handle is empty until a read or the commit
// starts it, which is what removes the separate beginTransaction round trip:
// the first read asks to start one and the reply carries the handle, and a
// closure that never reads folds its transaction into the commit instead. A Tx
// is used by one goroutine, the closure's, so this needs no lock.
type Tx struct {
	client   *Client
	handle   string
	readOnly bool
	closed   bool

	mutations []Mutation
}

// options are the TransactionOptions this transaction starts with, whether it
// starts inside a read or inside the commit.
func (t *Tx) options() json.RawMessage {
	if t.readOnly {
		return json.RawMessage(`{"readOnly":{}}`)
	}
	return json.RawMessage(`{"readWrite":{}}`)
}

// adopt records the handle a read's reply carried, if this read is what started
// the transaction. It is a no-op outside a transaction and on every read after
// the first.
func (t *Tx) adopt(handle string) {
	if t == nil || handle == "" || t.handle != "" {
		return
	}
	t.handle = handle
}

// Get reads inside the transaction.
func (t *Tx) Get(ctx context.Context, key Key) (*Entity, error) {
	if err := t.usable(); err != nil {
		return nil, err
	}
	result, err := t.client.lookup(ctx, []Key{key}, t, nil)
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
	return t.client.lookup(ctx, keys, t, nil)
}

// Run executes a query inside the transaction.
func (t *Tx) Run(ctx context.Context, q *Query) (*Batch, error) {
	if err := t.usable(); err != nil {
		return nil, err
	}
	return t.client.runQuery(ctx, q, t, nil)
}

// Count aggregates inside the transaction.
func (t *Tx) Count(ctx context.Context, q *Query) (int64, error) {
	if err := t.usable(); err != nil {
		return 0, err
	}
	v, err := t.client.aggregate(ctx, q, t, "count",
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
	return t.client.aggregate(ctx, q, t, "sum", wireAggregation{
		Alias: "sum",
		Sum:   &wirePropertyAggregation{Property: wirePropertyReference{Name: property}},
	}, nil)
}

// Avg averages one property inside the transaction.
func (t *Tx) Avg(ctx context.Context, q *Query, property string) (Value, error) {
	if err := t.usable(); err != nil {
		return Value{}, err
	}
	return t.client.aggregate(ctx, q, t, "avg", wireAggregation{
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
	// No beginTransaction here. The transaction starts inside whichever call
	// needs it first — a read, through readOptions.newTransaction, or the
	// commit, through singleUseTransaction — which is what makes one read plus
	// one commit cost two round trips instead of three.
	//
	// The saving is not confined to that shape. N reads cost N+1 rather than
	// N+2, a write-only closure costs one rather than three, and a closure that
	// neither reads nor writes costs none: there is no handle to release
	// because none was ever taken.
	tx := &Tx{client: c, readOnly: cfg.readOnly}
	committed := false
	defer func() {
		tx.closed = true
		if committed || tx.handle == "" {
			// Nothing to roll back when no call ever started a transaction.
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
			wireRollbackRequest{DatabaseID: c.database, Transaction: tx.handle}, nil)
	}()

	if err := fn(tx); err != nil {
		return err
	}
	if len(tx.mutations) == 0 {
		if tx.handle == "" {
			// A closure that neither read nor wrote. There is no snapshot to
			// release and no handle to release it with.
			committed = true
			return nil
		}
		// Reads happened, so a handle is open. Commit it empty to release the
		// snapshot rather than leaving it to time out.
		if err := c.releaseEmpty(ctx, tx); err != nil {
			return err
		}
		committed = true
		return nil
	}
	if _, err := c.commit(ctx, tx.mutations, tx, ""); err != nil {
		return err
	}
	committed = true
	return nil
}

// releaseEmpty commits a transaction that read but wrote nothing.
//
// commit returns early on an empty mutation list, since that is the ordinary
// no-op outside a transaction, so the release goes out from here instead.
func (c *Client) releaseEmpty(ctx context.Context, tx *Tx) error {
	req := wireCommitRequest{
		DatabaseID:  c.database,
		Mode:        "TRANSACTIONAL",
		Transaction: tx.handle,
		Mutations:   []wireMutation{},
	}
	return c.call(ctx, "commit", "", req, nil)
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
