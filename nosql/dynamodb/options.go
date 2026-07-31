package dynamodb

// Operation options.
//
// Each option carries the operations it is valid for in its type, so passing
// WithCondition to a Query is a compile error rather than a request DynamoDB
// rejects. The interfaces below are the operation groups; the option types
// after them are the capability sets.

// GetOption configures GetItem.
type GetOption interface{ applyGet(*opConfig) }

// WriteOption configures PutItem, UpdateItem and DeleteItem.
type WriteOption interface{ applyWrite(*opConfig) }

// QueryOption configures Query.
type QueryOption interface{ applyQuery(*opConfig) }

// ScanOption configures Scan.
type ScanOption interface{ applyScan(*opConfig) }

// BatchOption configures BatchGetItem.
type BatchOption interface{ applyBatch(*opConfig) }

// ListOption configures ListTables.
type ListOption interface{ applyList(*opConfig) }

// opConfig collects what the options set, before it becomes a request.
type opConfig struct {
	consistentRead bool
	projection     string
	condition      string
	filter         string
	index          string
	returnValues   string
	limit          int
	scanForward    *bool
	startKey       Key
	startTable     string
	names          map[string]string
	values         map[string]AttributeValue
}

// The capability sets. A single option value implements the interfaces of every
// operation it applies to.
type (
	// readOption: GetItem, Query, Scan, BatchGetItem.
	readOption struct{ f func(*opConfig) }
	// nameOption: everything that takes an expression.
	nameOption struct{ f func(*opConfig) }
	// valueOption: the operations that take expression values.
	valueOption struct{ f func(*opConfig) }
	// writeOnlyOption: PutItem, UpdateItem, DeleteItem.
	writeOnlyOption struct{ f func(*opConfig) }
	// scanRangeOption: Query and Scan.
	scanRangeOption struct{ f func(*opConfig) }
	// pageOption: Query, Scan and ListTables.
	pageOption struct{ f func(*opConfig) }
	// queryOnlyOption: Query.
	queryOnlyOption struct{ f func(*opConfig) }
	// listOnlyOption: ListTables.
	listOnlyOption struct{ f func(*opConfig) }
)

func (o readOption) applyGet(c *opConfig)   { o.f(c) }
func (o readOption) applyQuery(c *opConfig) { o.f(c) }
func (o readOption) applyScan(c *opConfig)  { o.f(c) }
func (o readOption) applyBatch(c *opConfig) { o.f(c) }

func (o nameOption) applyGet(c *opConfig)   { o.f(c) }
func (o nameOption) applyWrite(c *opConfig) { o.f(c) }
func (o nameOption) applyQuery(c *opConfig) { o.f(c) }
func (o nameOption) applyScan(c *opConfig)  { o.f(c) }
func (o nameOption) applyBatch(c *opConfig) { o.f(c) }

func (o valueOption) applyWrite(c *opConfig) { o.f(c) }
func (o valueOption) applyQuery(c *opConfig) { o.f(c) }
func (o valueOption) applyScan(c *opConfig)  { o.f(c) }

func (o writeOnlyOption) applyWrite(c *opConfig) { o.f(c) }

func (o scanRangeOption) applyQuery(c *opConfig) { o.f(c) }
func (o scanRangeOption) applyScan(c *opConfig)  { o.f(c) }

func (o pageOption) applyQuery(c *opConfig) { o.f(c) }
func (o pageOption) applyScan(c *opConfig)  { o.f(c) }
func (o pageOption) applyList(c *opConfig)  { o.f(c) }

func (o queryOnlyOption) applyQuery(c *opConfig) { o.f(c) }

func (o listOnlyOption) applyList(c *opConfig) { o.f(c) }

// WithConsistentRead asks for a strongly consistent read, which costs twice the
// read units and cannot be used against a global secondary index.
func WithConsistentRead(consistent bool) readOption {
	return readOption{func(c *opConfig) { c.consistentRead = consistent }}
}

// WithProjection limits the attributes returned, as a projection expression:
// "pk, #n, profile.city". Names that collide with reserved words go through
// WithExpressionNames.
func WithProjection(expr string) readOption {
	return readOption{func(c *opConfig) { c.projection = expr }}
}

// WithExpressionNames supplies the #name placeholders an expression uses.
func WithExpressionNames(names map[string]string) nameOption {
	return nameOption{func(c *opConfig) { c.names = names }}
}

// WithExpressionValues supplies the :value placeholders an expression uses.
func WithExpressionValues(values map[string]AttributeValue) valueOption {
	return valueOption{func(c *opConfig) { c.values = values }}
}

// WithCondition guards a write with a condition expression, such as
// "attribute_not_exists(pk)". A failed condition is ErrConditionalCheck, which
// is the intended outcome rather than a fault.
func WithCondition(expr string) writeOnlyOption {
	return writeOnlyOption{func(c *opConfig) { c.condition = expr }}
}

// WithReturnValues asks for attributes back from a write: "ALL_OLD",
// "ALL_NEW", "UPDATED_OLD", "UPDATED_NEW", or "NONE".
func WithReturnValues(which string) writeOnlyOption {
	return writeOnlyOption{func(c *opConfig) { c.returnValues = which }}
}

// WithIndex reads through a secondary index instead of the table itself.
func WithIndex(name string) scanRangeOption {
	return scanRangeOption{func(c *opConfig) { c.index = name }}
}

// WithFilter drops items after they are read, as a filter expression. It saves
// bandwidth, not read capacity: the items are read and charged first.
func WithFilter(expr string) scanRangeOption {
	return scanRangeOption{func(c *opConfig) { c.filter = expr }}
}

// WithExclusiveStartKey continues from where a page stopped, using the
// LastEvaluatedKey of the previous Page.
func WithExclusiveStartKey(key Key) scanRangeOption {
	return scanRangeOption{func(c *opConfig) { c.startKey = key }}
}

// WithLimit bounds how many items one page evaluates. DynamoDB may return
// fewer, and a short page with a LastEvaluatedKey is not the end of the result.
func WithLimit(n int) pageOption {
	return pageOption{func(c *opConfig) { c.limit = n }}
}

// WithScanForward reads the sort key in ascending order when true, which is the
// default, and descending when false.
func WithScanForward(forward bool) queryOnlyOption {
	return queryOnlyOption{func(c *opConfig) { c.scanForward = &forward }}
}

// WithStartTable continues a table listing from the LastEvaluatedName of the
// previous TableList.
func WithStartTable(name string) listOnlyOption {
	return listOnlyOption{func(c *opConfig) { c.startTable = name }}
}

func newGetConfig(opts []GetOption) *opConfig {
	c := &opConfig{}
	for _, o := range opts {
		o.applyGet(c)
	}
	return c
}

func newWriteConfig(opts []WriteOption) *opConfig {
	c := &opConfig{}
	for _, o := range opts {
		o.applyWrite(c)
	}
	return c
}

func newQueryConfig(opts []QueryOption) *opConfig {
	c := &opConfig{}
	for _, o := range opts {
		o.applyQuery(c)
	}
	return c
}

func newScanConfig(opts []ScanOption) *opConfig {
	c := &opConfig{}
	for _, o := range opts {
		o.applyScan(c)
	}
	return c
}

func newBatchConfig(opts []BatchOption) *opConfig {
	c := &opConfig{}
	for _, o := range opts {
		o.applyBatch(c)
	}
	return c
}

func newListConfig(opts []ListOption) *opConfig {
	c := &opConfig{}
	for _, o := range opts {
		o.applyList(c)
	}
	return c
}
