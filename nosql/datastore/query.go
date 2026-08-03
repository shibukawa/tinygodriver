package datastore

import (
	"encoding/json"
	"errors"
)

// Query errors.
var (
	ErrNoKind      = errors.New("datastore: query has no kind")
	ErrBadOperator = errors.New("datastore: unknown filter operator")
)

// Operator is a property filter comparison.
type Operator string

// The property filter operators. Datastore composes filters with AND only;
// there is no OR on this wire.
const (
	LessThan         Operator = "LESS_THAN"
	LessThanOrEqual  Operator = "LESS_THAN_OR_EQUAL"
	GreaterThan      Operator = "GREATER_THAN"
	GreaterThanEqual Operator = "GREATER_THAN_OR_EQUAL"
	Equal            Operator = "EQUAL"
	NotEqual         Operator = "NOT_EQUAL"
	HasAncestor      Operator = "HAS_ANCESTOR"
	In               Operator = "IN"
	NotIn            Operator = "NOT_IN"
)

func (o Operator) valid() bool {
	switch o {
	case LessThan, LessThanOrEqual, GreaterThan, GreaterThanEqual,
		Equal, NotEqual, HasAncestor, In, NotIn:
		return true
	}
	return false
}

type filter struct {
	property string
	op       Operator
	value    Value
}

type order struct {
	property   string
	descending bool
}

// Query selects entities of one kind. It is a value worth building once and
// running repeatedly, which is why it is a type rather than a pile of options.
//
// Every method returns a new Query, so a partially built query can be shared
// without one caller's additions reaching another.
type Query struct {
	kind        string
	filters     []filter
	orders      []order
	projection  []string
	distinctOn  []string
	startCursor string
	endCursor   string
	offset      int32
	limit       int32
	keysOnly    bool
	err         error
}

// NewQuery starts a query over one kind.
func NewQuery(kind string) *Query {
	q := &Query{kind: kind}
	if kind == "" {
		q.err = ErrNoKind
	}
	return q
}

func (q *Query) clone() *Query {
	out := *q
	out.filters = append([]filter(nil), q.filters...)
	out.orders = append([]order(nil), q.orders...)
	out.projection = append([]string(nil), q.projection...)
	out.distinctOn = append([]string(nil), q.distinctOn...)
	return &out
}

// Filter adds a property comparison. Filters combine with AND.
func (q *Query) Filter(property string, op Operator, value Value) *Query {
	out := q.clone()
	if !op.valid() {
		out.err = ErrBadOperator
		return out
	}
	out.filters = append(out.filters, filter{property: property, op: op, value: value})
	return out
}

// Ancestor restricts the query to descendants of key, which is a filter on the
// key path rather than on a property.
func (q *Query) Ancestor(key Key) *Query {
	return q.Filter("__key__", HasAncestor, KeyValue(key))
}

// Order sorts ascending by property.
func (q *Query) Order(property string) *Query {
	out := q.clone()
	out.orders = append(out.orders, order{property: property})
	return out
}

// OrderDesc sorts descending by property.
func (q *Query) OrderDesc(property string) *Query {
	out := q.clone()
	out.orders = append(out.orders, order{property: property, descending: true})
	return out
}

// Project returns only the named properties. A projection query reads from an
// index, so every projected property must be indexed.
func (q *Query) Project(properties ...string) *Query {
	out := q.clone()
	out.projection = append(out.projection, properties...)
	return out
}

// KeysOnly returns keys without properties, which is the cheapest read there
// is. It is a projection on the special __key__ property.
func (q *Query) KeysOnly() *Query {
	out := q.clone()
	out.keysOnly = true
	return out
}

// DistinctOn collapses results that share the named properties.
func (q *Query) DistinctOn(properties ...string) *Query {
	out := q.clone()
	out.distinctOn = append(out.distinctOn, properties...)
	return out
}

// Limit caps the batch size. Zero means the server decides.
func (q *Query) Limit(n int32) *Query {
	out := q.clone()
	out.limit = n
	return out
}

// Offset skips results. The skipped entities are still read and still billed,
// so a cursor is the cheaper way to resume.
func (q *Query) Offset(n int32) *Query {
	out := q.clone()
	out.offset = n
	return out
}

// Start resumes from a cursor returned by a previous batch.
func (q *Query) Start(cursor Cursor) *Query {
	out := q.clone()
	out.startCursor = string(cursor)
	return out
}

// End stops at a cursor.
func (q *Query) End(cursor Cursor) *Query {
	out := q.clone()
	out.endCursor = string(cursor)
	return out
}

// Cursor is an opaque position in a result set. It is fed back through Start,
// the same shape the S3 and DynamoDB clients use, rather than hidden inside an
// iterator that makes round trips the caller cannot see.
type Cursor string

// MoreResults says why a batch ended.
type MoreResults string

// The batch termination reasons.
const (
	NotFinished            MoreResults = "NOT_FINISHED"
	MoreResultsAfterLimit  MoreResults = "MORE_RESULTS_AFTER_LIMIT"
	MoreResultsAfterCursor MoreResults = "MORE_RESULTS_AFTER_CURSOR"
	NoMoreResults          MoreResults = "NO_MORE_RESULTS"
)

// Batch is one page of query results.
type Batch struct {
	Entities  []Entity
	EndCursor Cursor
	More      MoreResults

	// SkippedResults counts entities the offset stepped over. They were read
	// and billed.
	SkippedResults int32
}

// HasMore reports whether running the query again from EndCursor could return
// anything.
func (b *Batch) HasMore() bool {
	if b == nil {
		return false
	}
	return b.More == NotFinished || b.More == MoreResultsAfterLimit
}

// wire shapes

type wirePropertyReference struct {
	Name string `json:"name"`
}

type wireProjection struct {
	Property wirePropertyReference `json:"property"`
}

type wirePropertyOrder struct {
	Property  wirePropertyReference `json:"property"`
	Direction string                `json:"direction"`
}

type wirePropertyFilter struct {
	Property wirePropertyReference `json:"property"`
	Op       string                `json:"op"`
	Value    json.RawMessage       `json:"value"`
}

type wireFilter struct {
	CompositeFilter *wireCompositeFilter `json:"compositeFilter,omitempty"`
	PropertyFilter  *wirePropertyFilter  `json:"propertyFilter,omitempty"`
}

type wireCompositeFilter struct {
	Op      string       `json:"op"`
	Filters []wireFilter `json:"filters"`
}

type wireKindExpression struct {
	Name string `json:"name"`
}

type wireQuery struct {
	Kind        []wireKindExpression    `json:"kind,omitempty"`
	Projection  []wireProjection        `json:"projection,omitempty"`
	Filter      *wireFilter             `json:"filter,omitempty"`
	Order       []wirePropertyOrder     `json:"order,omitempty"`
	DistinctOn  []wirePropertyReference `json:"distinctOn,omitempty"`
	StartCursor string                  `json:"startCursor,omitempty"`
	EndCursor   string                  `json:"endCursor,omitempty"`
	Offset      int32                   `json:"offset,omitempty"`
	Limit       *int32                  `json:"limit,omitempty"`
}

// wire builds the request query. Key values inside filters need the partition,
// which is why this takes one.
func (q *Query) wire(partition *wirePartitionID) (*wireQuery, error) {
	if q.err != nil {
		return nil, q.err
	}
	if q.kind == "" {
		return nil, ErrNoKind
	}
	out := &wireQuery{
		Kind:        []wireKindExpression{{Name: q.kind}},
		StartCursor: q.startCursor,
		EndCursor:   q.endCursor,
		Offset:      q.offset,
	}
	if q.limit > 0 {
		limit := q.limit
		out.Limit = &limit
	}
	projection := q.projection
	if q.keysOnly {
		projection = append([]string{"__key__"}, projection...)
	}
	for _, name := range projection {
		out.Projection = append(out.Projection, wireProjection{Property: wirePropertyReference{Name: name}})
	}
	for _, name := range q.distinctOn {
		out.DistinctOn = append(out.DistinctOn, wirePropertyReference{Name: name})
	}
	for _, o := range q.orders {
		direction := "ASCENDING"
		if o.descending {
			direction = "DESCENDING"
		}
		out.Order = append(out.Order, wirePropertyOrder{
			Property:  wirePropertyReference{Name: o.property},
			Direction: direction,
		})
	}

	filters := make([]wireFilter, 0, len(q.filters))
	for _, f := range q.filters {
		value := f.value
		// A key inside a filter needs the project the request is for, the same
		// as a key anywhere else.
		if value.Key != nil {
			withPartition := value.Key.wire(partition)
			raw, err := json.Marshal(withPartition)
			if err != nil {
				return nil, err
			}
			filters = append(filters, wireFilter{PropertyFilter: &wirePropertyFilter{
				Property: wirePropertyReference{Name: f.property},
				Op:       string(f.op),
				Value:    json.RawMessage(`{"keyValue":` + string(raw) + `}`),
			}})
			continue
		}
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		filters = append(filters, wireFilter{PropertyFilter: &wirePropertyFilter{
			Property: wirePropertyReference{Name: f.property},
			Op:       string(f.op),
			Value:    raw,
		}})
	}
	switch len(filters) {
	case 0:
	case 1:
		out.Filter = &filters[0]
	default:
		// Datastore composes with AND only.
		out.Filter = &wireFilter{CompositeFilter: &wireCompositeFilter{Op: "AND", Filters: filters}}
	}
	return out, nil
}

type wireQueryResultBatch struct {
	SkippedResults   int32              `json:"skippedResults"`
	EntityResultType string             `json:"entityResultType"`
	EntityResults    []wireEntityResult `json:"entityResults"`
	EndCursor        string             `json:"endCursor"`
	MoreResults      string             `json:"moreResults"`
}
