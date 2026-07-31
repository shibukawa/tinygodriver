package dynamodb

import (
	"context"
	"time"
)

// AttributeType is the type of a key attribute. Only these three can be keys.
type AttributeType string

// The key attribute types.
const (
	TypeString AttributeType = "S"
	TypeNumber AttributeType = "N"
	TypeBinary AttributeType = "B"
)

// BillingMode is how a table is charged.
type BillingMode string

// The billing modes. PayPerRequest needs no capacity planning and is the
// default here, because a table this client creates is usually a test table or
// a small one.
const (
	PayPerRequest BillingMode = "PAY_PER_REQUEST"
	Provisioned   BillingMode = "PROVISIONED"
)

// KeyAttribute is one attribute used as a key.
type KeyAttribute struct {
	Name string
	Type AttributeType
}

// SecondaryIndex defines a global or local secondary index at table creation.
type SecondaryIndex struct {
	Name         string
	PartitionKey KeyAttribute
	SortKey      *KeyAttribute

	// Projection is "ALL", "KEYS_ONLY", or "INCLUDE" with Include listing the
	// extra attributes. Empty means "ALL".
	Projection string
	Include    []string

	// ReadCapacity and WriteCapacity apply to a global index on a provisioned
	// table, and are ignored otherwise.
	ReadCapacity  int64
	WriteCapacity int64
}

// TableDefinition is what CreateTable creates.
type TableDefinition struct {
	Name         string
	PartitionKey KeyAttribute
	SortKey      *KeyAttribute

	// BillingMode defaults to PayPerRequest, where the capacities are ignored.
	BillingMode   BillingMode
	ReadCapacity  int64
	WriteCapacity int64

	GlobalIndexes []SecondaryIndex
	LocalIndexes  []SecondaryIndex
}

// TableDescription is what DescribeTable reports.
//
// ItemCount and SizeBytes are updated by DynamoDB about every six hours, so
// they describe the table as of some time ago, not as of the call.
type TableDescription struct {
	Name      string
	Status    string // "CREATING", "ACTIVE", "DELETING", ...
	ItemCount int64
	SizeBytes int64
	CreatedAt time.Time
	Keys      []KeyAttribute
}

// Active reports whether the table is ready for reads and writes.
func (d *TableDescription) Active() bool { return d.Status == "ACTIVE" }

// TableList is one page of ListTables.
type TableList struct {
	Names []string

	// LastEvaluatedName continues the listing through WithStartTable, and is
	// empty on the last page.
	LastEvaluatedName string
}

type attributeDefinition struct {
	AttributeName string        `json:"AttributeName"`
	AttributeType AttributeType `json:"AttributeType"`
}

type keySchemaElement struct {
	AttributeName string `json:"AttributeName"`
	KeyType       string `json:"KeyType"`
}

type provisionedThroughput struct {
	ReadCapacityUnits  int64 `json:"ReadCapacityUnits"`
	WriteCapacityUnits int64 `json:"WriteCapacityUnits"`
}

type projection struct {
	ProjectionType   string   `json:"ProjectionType"`
	NonKeyAttributes []string `json:"NonKeyAttributes,omitempty"`
}

type secondaryIndexWire struct {
	IndexName             string                 `json:"IndexName"`
	KeySchema             []keySchemaElement     `json:"KeySchema"`
	Projection            projection             `json:"Projection"`
	ProvisionedThroughput *provisionedThroughput `json:"ProvisionedThroughput,omitempty"`
}

type createTableRequest struct {
	TableName              string                 `json:"TableName"`
	AttributeDefinitions   []attributeDefinition  `json:"AttributeDefinitions"`
	KeySchema              []keySchemaElement     `json:"KeySchema"`
	BillingMode            BillingMode            `json:"BillingMode,omitempty"`
	ProvisionedThroughput  *provisionedThroughput `json:"ProvisionedThroughput,omitempty"`
	GlobalSecondaryIndexes []secondaryIndexWire   `json:"GlobalSecondaryIndexes,omitempty"`
	LocalSecondaryIndexes  []secondaryIndexWire   `json:"LocalSecondaryIndexes,omitempty"`
}

type tableNameRequest struct {
	TableName string `json:"TableName"`
}

type tableWire struct {
	TableName        string             `json:"TableName"`
	TableStatus      string             `json:"TableStatus"`
	ItemCount        int64              `json:"ItemCount"`
	TableSizeBytes   int64              `json:"TableSizeBytes"`
	CreationDateTime float64            `json:"CreationDateTime"`
	KeySchema        []keySchemaElement `json:"KeySchema"`
}

// describeTableResponse accepts both spellings: DescribeTable answers with
// Table, while CreateTable and UpdateTable answer with TableDescription.
type describeTableResponse struct {
	Table            *tableWire `json:"Table"`
	TableDescription *tableWire `json:"TableDescription"`
}

// table returns whichever member the reply carried.
func (r *describeTableResponse) table() tableWire {
	switch {
	case r.Table != nil:
		return *r.Table
	case r.TableDescription != nil:
		return *r.TableDescription
	}
	return tableWire{}
}

type listTablesResponse struct {
	TableNames             []string `json:"TableNames"`
	LastEvaluatedTableName string   `json:"LastEvaluatedTableName"`
}

// keySchema renders the partition and sort key in the order DynamoDB requires.
func keySchema(partition KeyAttribute, sort *KeyAttribute) []keySchemaElement {
	schema := []keySchemaElement{{AttributeName: partition.Name, KeyType: "HASH"}}
	if sort != nil {
		schema = append(schema, keySchemaElement{AttributeName: sort.Name, KeyType: "RANGE"})
	}
	return schema
}

// attributeDefs collects every attribute used as a key anywhere in the table,
// deduplicated: DynamoDB wants each named once, however many indexes use it.
func attributeDefs(def TableDefinition) []attributeDefinition {
	seen := map[string]bool{}
	var defs []attributeDefinition
	add := func(a KeyAttribute) {
		if a.Name == "" || seen[a.Name] {
			return
		}
		seen[a.Name] = true
		typ := a.Type
		if typ == "" {
			typ = TypeString
		}
		defs = append(defs, attributeDefinition{AttributeName: a.Name, AttributeType: typ})
	}

	add(def.PartitionKey)
	if def.SortKey != nil {
		add(*def.SortKey)
	}
	for _, indexes := range [][]SecondaryIndex{def.GlobalIndexes, def.LocalIndexes} {
		for _, index := range indexes {
			add(index.PartitionKey)
			if index.SortKey != nil {
				add(*index.SortKey)
			}
		}
	}
	return defs
}

func indexWire(indexes []SecondaryIndex, mode BillingMode) []secondaryIndexWire {
	if len(indexes) == 0 {
		return nil
	}
	out := make([]secondaryIndexWire, 0, len(indexes))
	for _, index := range indexes {
		proj := projection{ProjectionType: index.Projection, NonKeyAttributes: index.Include}
		if proj.ProjectionType == "" {
			proj.ProjectionType = "ALL"
		}
		wire := secondaryIndexWire{
			IndexName:  index.Name,
			KeySchema:  keySchema(index.PartitionKey, index.SortKey),
			Projection: proj,
		}
		if mode == Provisioned {
			wire.ProvisionedThroughput = &provisionedThroughput{
				ReadCapacityUnits:  orOne(index.ReadCapacity),
				WriteCapacityUnits: orOne(index.WriteCapacity),
			}
		}
		out = append(out, wire)
	}
	return out
}

func orOne(n int64) int64 {
	if n <= 0 {
		return 1
	}
	return n
}

// CreateTable creates a table and returns as soon as DynamoDB accepts the
// request, which is before the table is usable. Poll DescribeTable for Active.
func (c *Client) CreateTable(ctx context.Context, def TableDefinition) error {
	mode := def.BillingMode
	if mode == "" {
		mode = PayPerRequest
	}
	req := createTableRequest{
		TableName:              def.Name,
		AttributeDefinitions:   attributeDefs(def),
		KeySchema:              keySchema(def.PartitionKey, def.SortKey),
		BillingMode:            mode,
		GlobalSecondaryIndexes: indexWire(def.GlobalIndexes, mode),
		LocalSecondaryIndexes:  indexWire(def.LocalIndexes, mode),
	}
	if mode == Provisioned {
		req.ProvisionedThroughput = &provisionedThroughput{
			ReadCapacityUnits:  orOne(def.ReadCapacity),
			WriteCapacityUnits: orOne(def.WriteCapacity),
		}
	}
	return c.do(ctx, "CreateTable", def.Name, req, nil)
}

// DeleteTable removes a table and everything in it.
func (c *Client) DeleteTable(ctx context.Context, table string) error {
	return c.do(ctx, "DeleteTable", table, tableNameRequest{TableName: table}, nil)
}

// DescribeTable reports a table's state.
func (c *Client) DescribeTable(ctx context.Context, table string) (*TableDescription, error) {
	var resp describeTableResponse
	if err := c.do(ctx, "DescribeTable", table, tableNameRequest{TableName: table}, &resp); err != nil {
		return nil, err
	}
	wire := resp.table()
	desc := &TableDescription{
		Name:      wire.TableName,
		Status:    wire.TableStatus,
		ItemCount: wire.ItemCount,
		SizeBytes: wire.TableSizeBytes,
	}
	if wire.CreationDateTime > 0 {
		sec, frac := splitEpoch(wire.CreationDateTime)
		desc.CreatedAt = time.Unix(sec, frac).UTC()
	}
	for _, element := range wire.KeySchema {
		desc.Keys = append(desc.Keys, KeyAttribute{Name: element.AttributeName})
	}
	return desc, nil
}

// splitEpoch turns the fractional epoch seconds DynamoDB sends into the pair
// time.Unix takes.
func splitEpoch(v float64) (sec int64, nsec int64) {
	sec = int64(v)
	return sec, int64((v - float64(sec)) * 1e9)
}

// ListTables lists table names, one page per call.
func (c *Client) ListTables(ctx context.Context, opts ...ListOption) (*TableList, error) {
	cfg := newListConfig(opts)
	req := &wireRequest{}
	req.apply(cfg)

	var resp listTablesResponse
	if err := c.do(ctx, "ListTables", "", req, &resp); err != nil {
		return nil, err
	}
	return &TableList{Names: resp.TableNames, LastEvaluatedName: resp.LastEvaluatedTableName}, nil
}
