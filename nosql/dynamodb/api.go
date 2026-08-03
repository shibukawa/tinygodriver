package dynamodb

import (
	"context"
	"encoding/json"
	"errors"
)

var errNoHost = errors.New("dynamodb: endpoint has no host")

// wireRequest is the union of the members the item, query and scan operations
// send. Every member is omitted when empty, so one struct serves them all
// without sending anything the operation does not take.
type wireRequest struct {
	TableName                 string                    `json:"TableName,omitempty"`
	Key                       Key                       `json:"Key,omitempty"`
	Item                      Item                      `json:"Item,omitempty"`
	UpdateExpression          string                    `json:"UpdateExpression,omitempty"`
	ConditionExpression       string                    `json:"ConditionExpression,omitempty"`
	KeyConditionExpression    string                    `json:"KeyConditionExpression,omitempty"`
	FilterExpression          string                    `json:"FilterExpression,omitempty"`
	ProjectionExpression      string                    `json:"ProjectionExpression,omitempty"`
	ExpressionAttributeNames  map[string]string         `json:"ExpressionAttributeNames,omitempty"`
	ExpressionAttributeValues map[string]AttributeValue `json:"ExpressionAttributeValues,omitempty"`
	ConsistentRead            bool                      `json:"ConsistentRead,omitempty"`
	IndexName                 string                    `json:"IndexName,omitempty"`
	Limit                     int                       `json:"Limit,omitempty"`
	ScanIndexForward          *bool                     `json:"ScanIndexForward,omitempty"`
	ExclusiveStartKey         Key                       `json:"ExclusiveStartKey,omitempty"`
	ReturnValues              string                    `json:"ReturnValues,omitempty"`
	ExclusiveStartTableName   string                    `json:"ExclusiveStartTableName,omitempty"`
}

// apply copies the option state onto the request.
func (r *wireRequest) apply(c *opConfig) {
	r.ConsistentRead = c.consistentRead
	r.ProjectionExpression = c.projection
	r.ConditionExpression = c.condition
	r.FilterExpression = c.filter
	r.IndexName = c.index
	r.ReturnValues = c.returnValues
	r.Limit = c.limit
	r.ScanIndexForward = c.scanForward
	r.ExclusiveStartKey = c.startKey
	r.ExclusiveStartTableName = c.startTable
	r.ExpressionAttributeNames = c.names
	r.ExpressionAttributeValues = c.values
}

// wireResponse is the union of the members the same operations return.
type wireResponse struct {
	Item             Item   `json:"Item"`
	Items            []Item `json:"Items"`
	Attributes       Item   `json:"Attributes"`
	LastEvaluatedKey Key    `json:"LastEvaluatedKey"`
	Count            int    `json:"Count"`
	ScannedCount     int    `json:"ScannedCount"`
}

// WriteResult is what a write reports back. Attributes is populated only when
// the call asked for it with WithReturnValues.
type WriteResult struct {
	Attributes Item
}

// Page is one page of a Query or Scan.
//
// LastEvaluatedKey is the continuation: non-nil means there is more, whatever
// Count says, and it feeds WithExclusiveStartKey. An empty page with a
// continuation key is normal, and happens when a filter dropped everything the
// page read.
type Page struct {
	Items            []Item
	LastEvaluatedKey Key
	Count            int
	ScannedCount     int
}

// HasMore reports whether another page follows this one.
func (p *Page) HasMore() bool { return len(p.LastEvaluatedKey) > 0 }

// GetItem reads one item by primary key.
//
// A key that matches nothing is ErrItemNotFound, not an empty item: DynamoDB
// answers a miss with a 200 and no Item member, which is too easy to read as an
// item with no attributes.
func (c *Client) GetItem(ctx context.Context, table string, key Key, opts ...GetOption) (Item, error) {
	req := &wireRequest{TableName: table, Key: key}
	req.apply(newGetConfig(opts))

	var resp wireResponse
	if err := c.do(ctx, "GetItem", table, req, &resp); err != nil {
		return nil, err
	}
	if resp.Item == nil {
		return nil, &Error{Op: "GetItem", Table: table, StatusCode: 200, err: ErrItemNotFound}
	}
	return resp.Item, nil
}

// PutItem writes one item, replacing any item with the same key.
//
// WithCondition("attribute_not_exists(pk)") turns it into an insert that fails
// with ErrConditionalCheck instead of overwriting.
func (c *Client) PutItem(ctx context.Context, table string, item Item, opts ...WriteOption) (*WriteResult, error) {
	req := &wireRequest{TableName: table, Item: item}
	req.apply(newWriteConfig(opts))
	return c.write(ctx, "PutItem", table, req)
}

// UpdateItem applies an update expression to one item, creating it when it does
// not exist. The expression is the DynamoDB syntax: "SET #n = :name",
// "ADD score :delta", "REMOVE obsolete".
//
// An ADD expression is not idempotent, so pair it with WithCondition when the
// caller cannot tolerate the request arriving twice. See WithRetry.
func (c *Client) UpdateItem(ctx context.Context, table string, key Key, update string, opts ...WriteOption) (*WriteResult, error) {
	req := &wireRequest{TableName: table, Key: key, UpdateExpression: update}
	req.apply(newWriteConfig(opts))
	return c.write(ctx, "UpdateItem", table, req)
}

// DeleteItem removes one item by primary key. Deleting an item that is not
// there succeeds.
func (c *Client) DeleteItem(ctx context.Context, table string, key Key, opts ...WriteOption) (*WriteResult, error) {
	req := &wireRequest{TableName: table, Key: key}
	req.apply(newWriteConfig(opts))
	return c.write(ctx, "DeleteItem", table, req)
}

func (c *Client) write(ctx context.Context, op, table string, req *wireRequest) (*WriteResult, error) {
	var resp wireResponse
	if err := c.do(ctx, op, table, req, &resp); err != nil {
		return nil, err
	}
	return &WriteResult{Attributes: resp.Attributes}, nil
}

// Query reads items sharing a partition key, as one page.
//
// keyCond is a key condition expression: "pk = :pk", or
// "pk = :pk AND begins_with(sk, :prefix)". Its placeholders come from
// WithExpressionValues.
//
// There is no paginator: a truncated page carries LastEvaluatedKey, which feeds
// WithExclusiveStartKey, so the request loop stays where the caller can see it.
func (c *Client) Query(ctx context.Context, table, keyCond string, opts ...QueryOption) (*Page, error) {
	req := &wireRequest{TableName: table, KeyConditionExpression: keyCond}
	req.apply(newQueryConfig(opts))
	return c.page(ctx, "Query", table, req)
}

// Scan reads every item in a table or index, as one page. It reads and charges
// for everything it touches, so a filter is not a substitute for a Query.
func (c *Client) Scan(ctx context.Context, table string, opts ...ScanOption) (*Page, error) {
	req := &wireRequest{TableName: table}
	req.apply(newScanConfig(opts))
	return c.page(ctx, "Scan", table, req)
}

func (c *Client) page(ctx context.Context, op, table string, req *wireRequest) (*Page, error) {
	var resp wireResponse
	if err := c.do(ctx, op, table, req, &resp); err != nil {
		return nil, err
	}
	return &Page{
		Items:            resp.Items,
		LastEvaluatedKey: resp.LastEvaluatedKey,
		Count:            resp.Count,
		ScannedCount:     resp.ScannedCount,
	}, nil
}

// WriteRequest is one member of a BatchWriteItem: either a put or a delete.
// Build it with PutRequest or DeleteRequest.
type WriteRequest struct {
	Put    Item
	Delete Key
}

// PutRequest stores item as part of a batch.
func PutRequest(item Item) WriteRequest { return WriteRequest{Put: item} }

// DeleteRequest removes key as part of a batch.
func DeleteRequest(key Key) WriteRequest { return WriteRequest{Delete: key} }

// MarshalJSON writes the PutRequest or DeleteRequest wrapper the batch API
// expects.
func (w WriteRequest) MarshalJSON() ([]byte, error) {
	switch {
	case w.Put != nil && w.Delete != nil:
		return nil, errors.New("dynamodb: write request is both a put and a delete")
	case w.Put != nil:
		return json.Marshal(map[string]map[string]Item{"PutRequest": {"Item": w.Put}})
	case w.Delete != nil:
		return json.Marshal(map[string]map[string]Key{"DeleteRequest": {"Key": w.Delete}})
	}
	return nil, errors.New("dynamodb: write request is neither a put nor a delete")
}

// UnmarshalJSON reads the same wrapper, which is how unprocessed items come
// back in a shape that can be sent again.
func (w *WriteRequest) UnmarshalJSON(data []byte) error {
	var wrapper struct {
		Put *struct {
			Item Item `json:"Item"`
		} `json:"PutRequest"`
		Delete *struct {
			Key Key `json:"Key"`
		} `json:"DeleteRequest"`
	}
	if err := json.Unmarshal(data, &wrapper); err != nil {
		return err
	}
	*w = WriteRequest{}
	if wrapper.Put != nil {
		w.Put = wrapper.Put.Item
	}
	if wrapper.Delete != nil {
		w.Delete = wrapper.Delete.Key
	}
	return nil
}

// BatchGetResult is what BatchGetItem returned, per table.
//
// UnprocessedKeys are the keys DynamoDB declined to read this time, usually
// because the batch hit a throughput or size limit. They are returned rather
// than retried inside the call: a partial success is a result, and whether the
// rest still matters is the caller's decision.
type BatchGetResult struct {
	Items           map[string][]Item
	UnprocessedKeys map[string][]Key
}

// HasUnprocessed reports whether any key went unread.
func (r *BatchGetResult) HasUnprocessed() bool {
	for _, keys := range r.UnprocessedKeys {
		if len(keys) > 0 {
			return true
		}
	}
	return false
}

// BatchWriteResult is what BatchWriteItem returned.
//
// UnprocessedItems carries the writes that did not happen, in a form that can
// be passed straight back to BatchWriteItem.
type BatchWriteResult struct {
	UnprocessedItems map[string][]WriteRequest
}

// HasUnprocessed reports whether any write was declined.
func (r *BatchWriteResult) HasUnprocessed() bool {
	for _, writes := range r.UnprocessedItems {
		if len(writes) > 0 {
			return true
		}
	}
	return false
}

type batchGetRequest struct {
	RequestItems map[string]batchGetTable `json:"RequestItems"`
}

type batchGetTable struct {
	Keys                     []Key             `json:"Keys"`
	ConsistentRead           bool              `json:"ConsistentRead,omitempty"`
	ProjectionExpression     string            `json:"ProjectionExpression,omitempty"`
	ExpressionAttributeNames map[string]string `json:"ExpressionAttributeNames,omitempty"`
}

type batchGetResponse struct {
	Responses       map[string][]Item        `json:"Responses"`
	UnprocessedKeys map[string]batchGetTable `json:"UnprocessedKeys"`
}

// Service limits, from the published quotas. They are exported because a
// caller batching work has to chunk against them, and a number copied out of
// the documentation into every consumer drifts silently when the service
// changes it.
const (
	// MaxBatchGet is the most items one BatchGetItem accepts, across tables.
	MaxBatchGet = 100

	// MaxBatchWrite is the most put and delete requests one BatchWriteItem
	// accepts, across tables.
	MaxBatchWrite = 25

	// MaxItemBytes is the largest a single item may be.
	MaxItemBytes = 400 << 10

	// MaxRequestBytes bounds one API request.
	MaxRequestBytes = 16 << 20
)

// BatchGetItem reads up to MaxBatchGet items across tables in one request.
//
// One handshake and one round trip serve the whole batch, which is what makes
// it worth more than the sum of its GetItems.
func (c *Client) BatchGetItem(ctx context.Context, keys map[string][]Key, opts ...BatchOption) (*BatchGetResult, error) {
	cfg := newBatchConfig(opts)
	req := batchGetRequest{RequestItems: make(map[string]batchGetTable, len(keys))}
	for table, tableKeys := range keys {
		req.RequestItems[table] = batchGetTable{
			Keys:                     tableKeys,
			ConsistentRead:           cfg.consistentRead,
			ProjectionExpression:     cfg.projection,
			ExpressionAttributeNames: cfg.names,
		}
	}

	var resp batchGetResponse
	if err := c.do(ctx, "BatchGetItem", firstTable(keys), req, &resp); err != nil {
		return nil, err
	}
	out := &BatchGetResult{Items: resp.Responses, UnprocessedKeys: map[string][]Key{}}
	for table, unprocessed := range resp.UnprocessedKeys {
		out.UnprocessedKeys[table] = unprocessed.Keys
	}
	return out, nil
}

type batchWriteRequest struct {
	RequestItems map[string][]WriteRequest `json:"RequestItems"`
}

type batchWriteResponse struct {
	UnprocessedItems map[string][]WriteRequest `json:"UnprocessedItems"`
}

// BatchWriteItem puts and deletes up to MaxBatchWrite items across tables in one request.
//
// It is not a transaction: the writes succeed or fail one by one, and the ones
// that did not happen come back in UnprocessedItems. Conditions are not
// available here, which is the price of the batching.
func (c *Client) BatchWriteItem(ctx context.Context, writes map[string][]WriteRequest) (*BatchWriteResult, error) {
	var resp batchWriteResponse
	req := batchWriteRequest{RequestItems: writes}
	if err := c.do(ctx, "BatchWriteItem", firstTable(writes), req, &resp); err != nil {
		return nil, err
	}
	if resp.UnprocessedItems == nil {
		resp.UnprocessedItems = map[string][]WriteRequest{}
	}
	return &BatchWriteResult{UnprocessedItems: resp.UnprocessedItems}, nil
}

// firstTable names a batch in an error message. A batch spans tables, so this
// is a label rather than the subject of the request.
func firstTable[T any](m map[string][]T) string {
	for table := range m {
		return table
	}
	return ""
}
