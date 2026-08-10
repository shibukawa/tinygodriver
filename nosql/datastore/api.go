package datastore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// Service limits, from the published quotas. They are exported because a
// caller batching work has to chunk against them, and a number copied out of
// the documentation into every consumer drifts silently when the service
// changes it.
//
// There is deliberately no maximum-mutations-per-commit constant: Google
// documents no count limit on a commit. A commit is bounded in bytes, by
// MaxRequestBytes and, inside a transaction, MaxTransactionBytes. The only
// documented count of 500 is property transformations per entity, which
// requirement:datastore-client-scope excludes. So chunk a batch write by size,
// not by count.
const (
	// MaxLookupKeys is the most keys one lookup accepts. GetMulti checks this
	// before sending.
	MaxLookupKeys = 1000

	// MaxRequestBytes bounds one API request.
	MaxRequestBytes = 10 << 20

	// MaxTransactionBytes bounds everything one transaction writes.
	MaxTransactionBytes = 10 << 20

	// MaxEntityBytes is the largest a single entity may be.
	MaxEntityBytes = 1<<20 - 4

	// MaxKeyBytes is the largest a single key may be.
	MaxKeyBytes = 6 << 10

	// MaxIndexedStringBytes is where a string property stops being indexed.
	// A longer value is still stored; it just cannot be filtered or ordered on.
	MaxIndexedStringBytes = 1500

	// MaxNestingDepth is how deep entity values may nest.
	MaxNestingDepth = 20
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
	configErr   error
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
	m.configErr = cfg.err
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
	if m.configErr != nil {
		return out, m.configErr
	}
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

// commit writes ms, inside tx or not.
//
// A tx whose reads already started the transaction commits against its handle.
// A tx that never read carries its options as singleUseTransaction, so a
// write-only closure is atomic in one round trip rather than three; see
// requirement:datastore-single-use-transaction.
func (c *Client) commit(ctx context.Context, ms []Mutation, tx *Tx, kind string) (*CommitResult, error) {
	if len(ms) == 0 {
		return &CommitResult{}, nil
	}
	req := wireCommitRequest{DatabaseID: c.database, Mutations: make([]wireMutation, 0, len(ms))}
	switch {
	case tx == nil:
		req.Mode = "NON_TRANSACTIONAL"
	case tx.handle != "":
		req.Mode = "TRANSACTIONAL"
		req.Transaction = tx.handle
	default:
		req.Mode = "TRANSACTIONAL"
		req.SingleUseTransaction = tx.options()
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
	result, err := c.commit(ctx, []Mutation{m}, nil, kind)
	if err != nil {
		return Key{}, err
	}
	if len(result.Keys) == 0 {
		return Key{}, nil
	}
	return result.Keys[0], nil
}

// MutationSize reports how many bytes m contributes to a commit request.
//
// It exists so a caller chunking a batch write against MaxRequestBytes does not
// have to marshal each entity itself and then hand it to this package to be
// marshalled again. Datastore publishes no per-commit mutation count, so size
// is the only bound there is to chunk against.
//
// This is the encoded mutation, including the key with its project, database
// and namespace attached — which is why it is a method on Client rather than on
// Entity: only the client knows the partition, and an Entity-level figure would
// understate every mutation by exactly the part the caller cannot see.
func (c *Client) MutationSize(m Mutation) (int, error) {
	encoded, err := c.encodeMutation(m)
	if err != nil {
		return 0, err
	}
	raw, err := json.Marshal(encoded)
	if err != nil {
		return 0, err
	}
	return len(raw), nil
}

// CommitOverheadBytes reports how many bytes a commit of n mutations spends on
// everything that is not the mutations themselves: the mode, the array that
// holds them, the comma between each pair, and the databaseId when this client
// has one.
//
// MutationSize measures a mutation; this measures the request built around
// them, which is the part a caller summing MutationSize cannot see. Together
// they account for the whole body:
//
//	c.CommitOverheadBytes(len(ms)) + Σ c.MutationSize(m) == bytes sent to :commit
//
// It takes a count rather than the mutations so that chunking stays a running
// total. A caller asks whether one more fits by adding that one's MutationSize
// and re-reading the overhead for n+1, instead of re-measuring the batch on
// every step.
//
// A named database is counted twice over, and both are real: once inside each
// mutation, because every key carries the partition, and once here for the
// request-level databaseId.
//
// This is the non-transactional envelope. A commit inside a transaction also
// carries a handle or a singleUseTransaction block, and only the transaction
// knows which; use Tx.CommitOverheadBytes there.
func (c *Client) CommitOverheadBytes(n int) int {
	return commitOverhead(wireCommitRequest{
		DatabaseID: c.database,
		Mode:       "NON_TRANSACTIONAL",
		Mutations:  []wireMutation{},
	}, n)
}

// commitOverhead marshals req with no mutations and adds the commas that n of
// them need between them.
//
// It measures the real request struct rather than returning a constant, so a
// field added to the wire shape is counted here without anyone having to
// remember this function exists. A constant is how the caller got the wrong
// number in the first place.
func commitOverhead(req wireCommitRequest, n int) int {
	raw, err := json.Marshal(req)
	if err != nil {
		return 0
	}
	if n < 2 {
		return len(raw)
	}
	return len(raw) + n - 1
}

// Mutate applies several mutations in one commit.
//
// Without a transaction this is NON_TRANSACTIONAL, where the server requires
// that no two mutations touch the same entity and does not promise all-or-none.
// Inside RunInTransaction it is atomic.
func (c *Client) Mutate(ctx context.Context, ms []Mutation) (*CommitResult, error) {
	return c.commit(ctx, ms, nil, "")
}

type wireReadOptions struct {
	ReadConsistency string          `json:"readConsistency,omitempty"`
	Transaction     string          `json:"transaction,omitempty"`
	NewTransaction  json.RawMessage `json:"newTransaction,omitempty"`
	ReadTime        string          `json:"readTime,omitempty"`
}

// buildReadOptions renders the read options for a read, inside tx or not.
//
// A transaction that has not started yet asks the read to start it, which is
// what removes the separate beginTransaction round trip; the reply carries the
// handle. See requirement:datastore-single-use-transaction.
func buildReadOptions(tx *Tx, opts []ReadOption) (*wireReadOptions, error) {
	var cfg readConfig
	for _, o := range opts {
		o.applyRead(&cfg)
	}
	if cfg.err != nil {
		return nil, cfg.err
	}
	if tx != nil && cfg.mode != readModeDefault {
		return nil, ErrConflictingReadOptions
	}
	out := &wireReadOptions{ReadTime: cfg.readTime}
	switch {
	case tx == nil:
		// A transaction already fixes consistency, so naming one alongside it
		// is a request the server rejects. Outside one it is the caller's.
		if cfg.mode == readModeEventual {
			out.ReadConsistency = "EVENTUAL"
		}
	case tx.handle != "":
		out.Transaction = tx.handle
	default:
		out.NewTransaction = tx.options()
	}
	if out.ReadConsistency == "" && out.Transaction == "" && out.NewTransaction == nil && out.ReadTime == "" {
		return nil, nil
	}
	return out, nil
}

type wireLookupRequest struct {
	DatabaseID  string           `json:"databaseId,omitempty"`
	ReadOptions *wireReadOptions `json:"readOptions,omitempty"`
	Keys        []wireKey        `json:"keys"`
}

type wireLookupResponse struct {
	Found       []wireEntityResult `json:"found"`
	Missing     []wireEntityResult `json:"missing"`
	Deferred    []Key              `json:"deferred"`
	Transaction string             `json:"transaction"`
}

// Get reads one entity, returning ErrNoSuchEntity when it is absent.
func (c *Client) Get(ctx context.Context, key Key, opts ...ReadOption) (*Entity, error) {
	result, err := c.lookup(ctx, []Key{key}, nil, opts)
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
	return c.lookup(ctx, keys, nil, opts)
}

func (c *Client) lookup(ctx context.Context, keys []Key, tx *Tx, opts []ReadOption) (*LookupResult, error) {
	if len(keys) == 0 {
		return &LookupResult{}, nil
	}
	if len(keys) > MaxLookupKeys {
		return nil, fmt.Errorf("%w: got %d", ErrTooManyKeys, len(keys))
	}
	readOptions, err := buildReadOptions(tx, opts)
	if err != nil {
		return nil, err
	}
	req := wireLookupRequest{
		DatabaseID:  c.database,
		ReadOptions: readOptions,
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
	tx.adopt(resp.Transaction)
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
	Batch       wireQueryResultBatch `json:"batch"`
	Transaction string               `json:"transaction"`
}

// Run executes a query and returns one batch.
//
// There is no iterator hiding round trips: feed EndCursor back through
// Query.Start to continue, the same shape the S3 and DynamoDB clients use.
func (c *Client) Run(ctx context.Context, q *Query, opts ...ReadOption) (*Batch, error) {
	return c.runQuery(ctx, q, nil, opts)
}

func (c *Client) runQuery(ctx context.Context, q *Query, tx *Tx, opts []ReadOption) (*Batch, error) {
	if q == nil {
		return nil, ErrNoKind
	}
	partition := c.partition()
	wq, err := q.wire(partition)
	if err != nil {
		return nil, err
	}
	readOptions, err := buildReadOptions(tx, opts)
	if err != nil {
		return nil, err
	}
	req := wireRunQueryRequest{
		DatabaseID:  c.database,
		PartitionID: partition,
		ReadOptions: readOptions,
		Query:       wq,
	}
	var resp wireRunQueryResponse
	if err := c.call(ctx, "runQuery", q.kind, req, &resp); err != nil {
		return nil, err
	}
	tx.adopt(resp.Transaction)
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
	Alias string                   `json:"alias"`
	Count *wireCountAggregation    `json:"count,omitempty"`
	Sum   *wirePropertyAggregation `json:"sum,omitempty"`
	Avg   *wirePropertyAggregation `json:"avg,omitempty"`
}

type wireCountAggregation struct {
	UpTo string `json:"upTo,omitempty"`
}

type wirePropertyAggregation struct {
	Property wirePropertyReference `json:"property"`
}

type wireRunAggregationRequest struct {
	DatabaseID       string                `json:"databaseId,omitempty"`
	PartitionID      *wirePartitionID      `json:"partitionId,omitempty"`
	ReadOptions      *wireReadOptions      `json:"readOptions,omitempty"`
	AggregationQuery *wireAggregationQuery `json:"aggregationQuery"`
}

type wireRunAggregationResponse struct {
	Transaction string `json:"transaction"`
	Batch       struct {
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
	v, err := c.aggregate(ctx, q, nil, "count",
		wireAggregation{Alias: "count", Count: &wireCountAggregation{}}, opts)
	if err != nil {
		return 0, err
	}
	n, ok := v.AsInt()
	if !ok {
		return 0, fmt.Errorf("datastore: count was not an integer")
	}
	return n, nil
}

// Sum totals one property across the entities the query matches.
//
// The result is an integer when every summed value is an integer and a double
// otherwise, which is why this returns a Value rather than one Go type:
// flattening it would erase the same integer-versus-double distinction the
// rest of this package keeps. Values of other types are ignored by the service
// rather than failing the query.
//
// It is here for the reason Count is, applied consistently. Counting by paging
// can be done keys-only; summing by paging cannot, because every entity has to
// come back in full for the caller to add one property up. So paging to sum is
// strictly more expensive than paging to count, and the argument that put Count
// in scope applies harder here. requirement:datastore-client-scope excluded
// these as "conveniences over data the caller can page" until a downstream
// reader pointed that out on 2026-08-04.
func (c *Client) Sum(ctx context.Context, q *Query, property string, opts ...ReadOption) (Value, error) {
	return c.aggregate(ctx, q, nil, "sum", wireAggregation{
		Alias: "sum",
		Sum:   &wirePropertyAggregation{Property: wirePropertyReference{Name: property}},
	}, opts)
}

// Avg averages one property across the entities the query matches.
//
// The result is a double, or null when nothing matched — which is why this
// returns a Value too: zero would be a different claim from "no data".
func (c *Client) Avg(ctx context.Context, q *Query, property string, opts ...ReadOption) (Value, error) {
	return c.aggregate(ctx, q, nil, "avg", wireAggregation{
		Alias: "avg",
		Avg:   &wirePropertyAggregation{Property: wirePropertyReference{Name: property}},
	}, opts)
}

func (c *Client) aggregate(ctx context.Context, q *Query, tx *Tx, alias string,
	aggregation wireAggregation, opts []ReadOption) (Value, error) {
	if q == nil {
		return Value{}, ErrNoKind
	}
	if aggregation.Count == nil && aggregation.Sum == nil && aggregation.Avg == nil {
		return Value{}, fmt.Errorf("datastore: aggregation has no operator")
	}
	if (aggregation.Sum != nil && aggregation.Sum.Property.Name == "") ||
		(aggregation.Avg != nil && aggregation.Avg.Property.Name == "") {
		return Value{}, fmt.Errorf("datastore: %s needs a property name", alias)
	}
	partition := c.partition()
	wq, err := q.wire(partition)
	if err != nil {
		return Value{}, err
	}
	readOptions, err := buildReadOptions(tx, opts)
	if err != nil {
		return Value{}, err
	}
	req := wireRunAggregationRequest{
		DatabaseID:  c.database,
		PartitionID: partition,
		ReadOptions: readOptions,
		AggregationQuery: &wireAggregationQuery{
			NestedQuery:  wq,
			Aggregations: []wireAggregation{aggregation},
		},
	}
	var resp wireRunAggregationResponse
	if err := c.call(ctx, "runAggregationQuery", q.kind, req, &resp); err != nil {
		return Value{}, err
	}
	tx.adopt(resp.Transaction)
	if len(resp.Batch.AggregationResults) == 0 {
		return Value{}, fmt.Errorf("datastore: aggregation reply carried no result")
	}
	value, ok := resp.Batch.AggregationResults[0].AggregateProperties[alias]
	if !ok {
		return Value{}, fmt.Errorf("datastore: aggregation reply carried no %s", alias)
	}
	return value, nil
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
