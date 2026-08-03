package datastore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// Operation limits taken from the published quotas.
const (
	maxLookupKeys = 1000
)

// ErrTooManyKeys is returned before a request the server would reject.
var ErrTooManyKeys = errors.New("datastore: more than 1000 keys in one lookup")

// mutation verbs
type mutationKind int

const (
	mutationInsert mutationKind = iota
	mutationUpdate
	mutationUpsert
	mutationDelete
)

// Mutation is one change in a commit. Datastore carries the verb in the
// request rather than in the endpoint, so several verbs fit in one round trip.
type Mutation struct {
	kind        mutationKind
	entity      Entity
	key         Key
	baseVersion *int64
	updateTime  string
}

// InsertOp fails if the key already exists. This is put-if-absent, and it is
// the closest thing on this wire to a condition expression.
func InsertOp(e Entity) Mutation { return Mutation{kind: mutationInsert, entity: e} }

// UpdateOp fails if the key does not exist. This is put-if-present.
func UpdateOp(e Entity) Mutation { return Mutation{kind: mutationUpdate, entity: e} }

// UpsertOp writes unconditionally.
func UpsertOp(e Entity) Mutation { return Mutation{kind: mutationUpsert, entity: e} }

// DeleteOp removes an entity. Deleting an absent key succeeds, which is what
// makes it replay-safe.
func DeleteOp(k Key) Mutation { return Mutation{kind: mutationDelete, key: k} }

// With applies write options, so a Mutation inside Mutate can carry the same
// preconditions a single-entity call takes as arguments.
func (m Mutation) With(opts ...WriteOption) Mutation {
	var cfg writeConfig
	for _, o := range opts {
		o.applyWrite(&cfg)
	}
	m.baseVersion = cfg.baseVersion
	m.updateTime = cfg.updateTime
	return m
}

type wireMutation struct {
	Insert      json.RawMessage `json:"insert,omitempty"`
	Update      json.RawMessage `json:"update,omitempty"`
	Upsert      json.RawMessage `json:"upsert,omitempty"`
	Delete      *wireKey        `json:"delete,omitempty"`
	BaseVersion string          `json:"baseVersion,omitempty"`
	UpdateTime  string          `json:"updateTime,omitempty"`
}

func (c *Client) encodeMutation(m Mutation) (wireMutation, error) {
	var out wireMutation
	if m.baseVersion != nil {
		out.BaseVersion = fmt.Sprintf("%d", *m.baseVersion)
	}
	out.UpdateTime = m.updateTime

	if m.kind == mutationDelete {
		if err := m.key.Valid(); err != nil {
			return out, err
		}
		if m.key.Incomplete() {
			return out, fmt.Errorf("%w: cannot delete an incomplete key", ErrIncompleteKey)
		}
		key := c.encodeKey(m.key)
		out.Delete = &key
		return out, nil
	}

	if m.entity.Key == nil {
		return out, ErrEmptyKeyPath
	}
	if err := m.entity.Key.Valid(); err != nil {
		return out, err
	}
	// Only insert may carry an incomplete key: update and upsert have to name
	// something that already exists or is being named exactly.
	if m.entity.Key.Incomplete() && m.kind != mutationInsert {
		return out, fmt.Errorf("%w: only an insert may use one", ErrIncompleteKey)
	}
	body, err := c.encodeEntity(m.entity)
	if err != nil {
		return out, err
	}
	switch m.kind {
	case mutationInsert:
		out.Insert = body
	case mutationUpdate:
		out.Update = body
	case mutationUpsert:
		out.Upsert = body
	}
	return out, nil
}

type wireMutationResult struct {
	Key              *Key   `json:"key"`
	Version          string `json:"version"`
	CreateTime       string `json:"createTime"`
	UpdateTime       string `json:"updateTime"`
	ConflictDetected bool   `json:"conflictDetected"`
}

// CommitResult reports what a commit did.
type CommitResult struct {
	// Keys are the keys of the written entities, in mutation order. An insert
	// with an incomplete key comes back completed here.
	Keys []Key

	// Versions are the post-commit versions, in the same order.
	Versions []int64

	// IndexUpdates counts the index entries the commit touched, which is what
	// a write is billed on.
	IndexUpdates int

	CommitTime string
}

type wireCommitRequest struct {
	DatabaseID           string          `json:"databaseId,omitempty"`
	Mode                 string          `json:"mode"`
	Mutations            []wireMutation  `json:"mutations"`
	Transaction          string          `json:"transaction,omitempty"`
	SingleUseTransaction json.RawMessage `json:"singleUseTransaction,omitempty"`
}

type wireCommitResponse struct {
	MutationResults []wireMutationResult `json:"mutationResults"`
	IndexUpdates    int                  `json:"indexUpdates"`
	CommitTime      string               `json:"commitTime"`
}

func (c *Client) commit(ctx context.Context, ms []Mutation, transaction string, kind string) (*CommitResult, error) {
	if len(ms) == 0 {
		return &CommitResult{}, nil
	}
	req := wireCommitRequest{DatabaseID: c.database, Mutations: make([]wireMutation, 0, len(ms))}
	if transaction != "" {
		req.Mode = "TRANSACTIONAL"
		req.Transaction = transaction
	} else {
		req.Mode = "NON_TRANSACTIONAL"
	}
	for _, m := range ms {
		encoded, err := c.encodeMutation(m)
		if err != nil {
			return nil, err
		}
		req.Mutations = append(req.Mutations, encoded)
	}

	var resp wireCommitResponse
	if err := c.call(ctx, "commit", kind, req, &resp); err != nil {
		return nil, err
	}
	out := &CommitResult{
		IndexUpdates: resp.IndexUpdates,
		CommitTime:   resp.CommitTime,
		Keys:         make([]Key, 0, len(resp.MutationResults)),
		Versions:     make([]int64, 0, len(resp.MutationResults)),
	}
	for i, r := range resp.MutationResults {
		key := Key{}
		switch {
		case r.Key != nil:
			key = *r.Key
		case i < len(ms) && ms[i].kind == mutationDelete:
			key = ms[i].key
		case i < len(ms) && ms[i].entity.Key != nil:
			key = *ms[i].entity.Key
		}
		out.Keys = append(out.Keys, key)
		out.Versions = append(out.Versions, parseInt64(r.Version))
	}
	return out, nil
}

// Put writes an entity, creating or replacing it. It sends an upsert.
//
// Update replaces the whole entity: there is no partial update on this wire and
// no server-side arithmetic, which is also why a replayed Put cannot double
// anything.
func (c *Client) Put(ctx context.Context, e Entity, opts ...WriteOption) (Key, error) {
	return c.writeOne(ctx, UpsertOp(e).With(opts...))
}

// Insert writes an entity, failing with ErrAlreadyExists if the key is taken.
//
// An incomplete key is allowed here and only here; the allocated key comes back
// in the result.
func (c *Client) Insert(ctx context.Context, e Entity, opts ...WriteOption) (Key, error) {
	return c.writeOne(ctx, InsertOp(e).With(opts...))
}

// Update replaces an entity, failing with ErrNoSuchEntity if it is absent.
func (c *Client) Update(ctx context.Context, e Entity, opts ...WriteOption) error {
	_, err := c.writeOne(ctx, UpdateOp(e).With(opts...))
	return err
}

// Delete removes an entity. Deleting an absent key succeeds.
func (c *Client) Delete(ctx context.Context, k Key, opts ...WriteOption) error {
	_, err := c.writeOne(ctx, DeleteOp(k).With(opts...))
	return err
}

func (c *Client) writeOne(ctx context.Context, m Mutation) (Key, error) {
	kind := m.key.Kind()
	if m.kind != mutationDelete && m.entity.Key != nil {
		kind = m.entity.Key.Kind()
	}
	result, err := c.commit(ctx, []Mutation{m}, "", kind)
	if err != nil {
		return Key{}, err
	}
	if len(result.Keys) == 0 {
		return Key{}, nil
	}
	return result.Keys[0], nil
}

// Mutate applies several mutations in one commit.
//
// Without a transaction this is NON_TRANSACTIONAL, where the server requires
// that no two mutations touch the same entity and does not promise all-or-none.
// Inside RunInTransaction it is atomic.
func (c *Client) Mutate(ctx context.Context, ms []Mutation) (*CommitResult, error) {
	return c.commit(ctx, ms, "", "")
}

type wireReadOptions struct {
	ReadConsistency string `json:"readConsistency,omitempty"`
	Transaction     string `json:"transaction,omitempty"`
	ReadTime        string `json:"readTime,omitempty"`
}

func buildReadOptions(transaction string, opts []ReadOption) *wireReadOptions {
	var cfg readConfig
	for _, o := range opts {
		o.applyRead(&cfg)
	}
	out := &wireReadOptions{Transaction: transaction, ReadTime: cfg.readTime}
	// A transaction already fixes consistency, so naming one alongside it is a
	// request the server rejects.
	if transaction == "" && cfg.eventual {
		out.ReadConsistency = "EVENTUAL"
	}
	if out.ReadConsistency == "" && out.Transaction == "" && out.ReadTime == "" {
		return nil
	}
	return out
}

type wireLookupRequest struct {
	DatabaseID  string           `json:"databaseId,omitempty"`
	ReadOptions *wireReadOptions `json:"readOptions,omitempty"`
	Keys        []wireKey        `json:"keys"`
}

type wireLookupResponse struct {
	Found    []wireEntityResult `json:"found"`
	Missing  []wireEntityResult `json:"missing"`
	Deferred []Key              `json:"deferred"`
}

// Get reads one entity, returning ErrNoSuchEntity when it is absent.
func (c *Client) Get(ctx context.Context, key Key, opts ...ReadOption) (*Entity, error) {
	result, err := c.lookup(ctx, []Key{key}, "", opts)
	if err != nil {
		return nil, err
	}
	if len(result.Found) == 0 {
		return nil, &Error{Op: "lookup", Kind: key.Kind(), StatusCode: 404,
			Status: "NOT_FOUND", Message: "no entity for key " + key.String()}
	}
	return &result.Found[0], nil
}

// GetMulti reads several entities.
//
// It returns found, missing and deferred rather than failing, because the
// server answers a lookup by partitioning the keys. Deferred keys are handed
// back, not retried inside the call.
func (c *Client) GetMulti(ctx context.Context, keys []Key, opts ...ReadOption) (*LookupResult, error) {
	return c.lookup(ctx, keys, "", opts)
}

func (c *Client) lookup(ctx context.Context, keys []Key, transaction string, opts []ReadOption) (*LookupResult, error) {
	if len(keys) == 0 {
		return &LookupResult{}, nil
	}
	if len(keys) > maxLookupKeys {
		return nil, fmt.Errorf("%w: got %d", ErrTooManyKeys, len(keys))
	}
	req := wireLookupRequest{
		DatabaseID:  c.database,
		ReadOptions: buildReadOptions(transaction, opts),
		Keys:        make([]wireKey, 0, len(keys)),
	}
	for _, k := range keys {
		if err := k.Valid(); err != nil {
			return nil, err
		}
		if k.Incomplete() {
			return nil, fmt.Errorf("%w: cannot look up an incomplete key", ErrIncompleteKey)
		}
		req.Keys = append(req.Keys, c.encodeKey(k))
	}

	var resp wireLookupResponse
	if err := c.call(ctx, "lookup", keys[0].Kind(), req, &resp); err != nil {
		return nil, err
	}
	out := &LookupResult{Deferred: resp.Deferred}
	for _, r := range resp.Found {
		out.Found = append(out.Found, r.entity())
	}
	for _, r := range resp.Missing {
		if r.Entity.Key != nil {
			out.Missing = append(out.Missing, *r.Entity.Key)
		}
	}
	return out, nil
}

type wireRunQueryRequest struct {
	DatabaseID  string           `json:"databaseId,omitempty"`
	PartitionID *wirePartitionID `json:"partitionId,omitempty"`
	ReadOptions *wireReadOptions `json:"readOptions,omitempty"`
	Query       *wireQuery       `json:"query"`
}

type wireRunQueryResponse struct {
	Batch wireQueryResultBatch `json:"batch"`
}

// Run executes a query and returns one batch.
//
// There is no iterator hiding round trips: feed EndCursor back through
// Query.Start to continue, the same shape the S3 and DynamoDB clients use.
func (c *Client) Run(ctx context.Context, q *Query, opts ...ReadOption) (*Batch, error) {
	return c.runQuery(ctx, q, "", opts)
}

func (c *Client) runQuery(ctx context.Context, q *Query, transaction string, opts []ReadOption) (*Batch, error) {
	if q == nil {
		return nil, ErrNoKind
	}
	partition := c.partition()
	wq, err := q.wire(partition)
	if err != nil {
		return nil, err
	}
	req := wireRunQueryRequest{
		DatabaseID:  c.database,
		PartitionID: partition,
		ReadOptions: buildReadOptions(transaction, opts),
		Query:       wq,
	}
	var resp wireRunQueryResponse
	if err := c.call(ctx, "runQuery", q.kind, req, &resp); err != nil {
		return nil, err
	}
	out := &Batch{
		EndCursor:      Cursor(resp.Batch.EndCursor),
		More:           MoreResults(resp.Batch.MoreResults),
		SkippedResults: resp.Batch.SkippedResults,
	}
	for _, r := range resp.Batch.EntityResults {
		out.Entities = append(out.Entities, r.entity())
	}
	return out, nil
}

type wireAggregationQuery struct {
	NestedQuery  *wireQuery        `json:"nestedQuery"`
	Aggregations []wireAggregation `json:"aggregations"`
}

type wireAggregation struct {
	Alias string                `json:"alias"`
	Count *wireCountAggregation `json:"count,omitempty"`
}

type wireCountAggregation struct {
	UpTo string `json:"upTo,omitempty"`
}

type wireRunAggregationRequest struct {
	DatabaseID       string                `json:"databaseId,omitempty"`
	PartitionID      *wirePartitionID      `json:"partitionId,omitempty"`
	ReadOptions      *wireReadOptions      `json:"readOptions,omitempty"`
	AggregationQuery *wireAggregationQuery `json:"aggregationQuery"`
}

type wireRunAggregationResponse struct {
	Batch struct {
		AggregationResults []struct {
			AggregateProperties map[string]Value `json:"aggregateProperties"`
		} `json:"aggregationResults"`
	} `json:"batch"`
}

// Count returns how many entities the query matches.
//
// It exists because counting by paging through keys costs a read per entity,
// so leaving it out would push callers toward the expensive thing.
func (c *Client) Count(ctx context.Context, q *Query, opts ...ReadOption) (int64, error) {
	return c.count(ctx, q, "", opts)
}

func (c *Client) count(ctx context.Context, q *Query, transaction string, opts []ReadOption) (int64, error) {
	if q == nil {
		return 0, ErrNoKind
	}
	partition := c.partition()
	wq, err := q.wire(partition)
	if err != nil {
		return 0, err
	}
	req := wireRunAggregationRequest{
		DatabaseID:  c.database,
		PartitionID: partition,
		ReadOptions: buildReadOptions(transaction, opts),
		AggregationQuery: &wireAggregationQuery{
			NestedQuery:  wq,
			Aggregations: []wireAggregation{{Alias: "count", Count: &wireCountAggregation{}}},
		},
	}
	var resp wireRunAggregationResponse
	if err := c.call(ctx, "runAggregationQuery", q.kind, req, &resp); err != nil {
		return 0, err
	}
	if len(resp.Batch.AggregationResults) == 0 {
		return 0, nil
	}
	value, ok := resp.Batch.AggregationResults[0].AggregateProperties["count"]
	if !ok {
		return 0, fmt.Errorf("datastore: aggregation reply carried no count")
	}
	n, ok := value.AsInt()
	if !ok {
		return 0, fmt.Errorf("datastore: count was not an integer")
	}
	return n, nil
}

type wireAllocateIDsRequest struct {
	DatabaseID string    `json:"databaseId,omitempty"`
	Keys       []wireKey `json:"keys"`
}

type wireAllocateIDsResponse struct {
	Keys []Key `json:"keys"`
}

// AllocateIDs completes incomplete keys, for referring to an entity before
// writing it.
func (c *Client) AllocateIDs(ctx context.Context, keys []Key) ([]Key, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	req := wireAllocateIDsRequest{DatabaseID: c.database, Keys: make([]wireKey, 0, len(keys))}
	for _, k := range keys {
		if err := k.Valid(); err != nil {
			return nil, err
		}
		if !k.Incomplete() {
			return nil, fmt.Errorf("%w: AllocateIDs needs incomplete keys, %s is complete",
				ErrAmbiguousID, k.String())
		}
		req.Keys = append(req.Keys, c.encodeKey(k))
	}
	var resp wireAllocateIDsResponse
	if err := c.call(ctx, "allocateIds", keys[0].Kind(), req, &resp); err != nil {
		return nil, err
	}
	return resp.Keys, nil
}
