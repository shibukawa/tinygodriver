package datastore

import (
	"encoding/json"
	"errors"
	"fmt"
)

// MaxDisjunctions is how many disjunctions a query may expand to once its
// filter is put in disjunctive normal form. A query past it is rejected by the
// service, not here: the expansion rule is the service's and a client-side
// count that disagreed would refuse a query that works.
const MaxDisjunctions = 30

// ErrEmptyCondition is returned for And or Or with no operands, which has no
// meaning on the wire.
var ErrEmptyCondition = errors.New("datastore: And or Or with no conditions")

// Condition is a filter tree. Build one with Prop, And and Or, and attach it
// with Query.Where.
//
// Datastore composes with AND and OR; the OR arm arrived with disjunctive
// queries and this package believed otherwise until 2026-08-04, when a
// downstream reader asked whether the AND-only comment was still true. It was
// not.
type Condition interface {
	// wire renders the condition, given the partition a key value needs.
	wire(partition *wirePartitionID) (wireFilter, error)
	// count reports how many property filters the tree holds, so an empty
	// composite can be rejected before it reaches the service.
	count() int
}

type propCondition struct {
	property string
	op       Operator
	value    Value
	err      error
}

type compositeCondition struct {
	op    string
	parts []Condition
}

// Prop compares one property.
func Prop(property string, op Operator, value Value) Condition {
	c := propCondition{property: property, op: op, value: value}
	if !op.valid() {
		c.err = ErrBadOperator
	}
	if property == "" {
		c.err = fmt.Errorf("%w: filter has no property", ErrBadOperator)
	}
	return c
}

// Ancestor restricts results to descendants of key. It is a filter on the key
// path rather than on a property, which is why it reads as a condition rather
// than as a query option.
func AncestorOf(key Key) Condition { return Prop("__key__", HasAncestor, KeyValue(key)) }

// And requires every condition.
func And(conds ...Condition) Condition {
	return compositeCondition{op: "AND", parts: conds}
}

// Or requires at least one condition.
//
// A query using it counts against MaxDisjunctions once the whole filter is put
// in disjunctive normal form, so nesting Or inside And multiplies rather than
// adds.
func Or(conds ...Condition) Condition {
	return compositeCondition{op: "OR", parts: conds}
}

func (c propCondition) count() int { return 1 }

func (c propCondition) wire(partition *wirePartitionID) (wireFilter, error) {
	if c.err != nil {
		return wireFilter{}, c.err
	}
	var raw json.RawMessage
	if c.value.Key != nil {
		// A key inside a filter needs the project the request is for, the same
		// as a key anywhere else.
		encoded, err := json.Marshal(c.value.Key.wire(partition))
		if err != nil {
			return wireFilter{}, err
		}
		raw = json.RawMessage(`{"keyValue":` + string(encoded) + `}`)
	} else {
		encoded, err := json.Marshal(c.value)
		if err != nil {
			return wireFilter{}, err
		}
		raw = encoded
	}
	return wireFilter{PropertyFilter: &wirePropertyFilter{
		Property: wirePropertyReference{Name: c.property},
		Op:       string(c.op),
		Value:    raw,
	}}, nil
}

func (c compositeCondition) count() int {
	n := 0
	for _, part := range c.parts {
		if part != nil {
			n += part.count()
		}
	}
	return n
}

func (c compositeCondition) wire(partition *wirePartitionID) (wireFilter, error) {
	parts := make([]wireFilter, 0, len(c.parts))
	for _, part := range c.parts {
		if part == nil {
			continue
		}
		rendered, err := part.wire(partition)
		if err != nil {
			return wireFilter{}, err
		}
		parts = append(parts, rendered)
	}
	switch len(parts) {
	case 0:
		return wireFilter{}, ErrEmptyCondition
	case 1:
		// A composite of one is the operand. Sending the wrapper would be
		// legal and would read as if the tree meant more than it does.
		return parts[0], nil
	}
	return wireFilter{CompositeFilter: &wireCompositeFilter{Op: c.op, Filters: parts}}, nil
}
