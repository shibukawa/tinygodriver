package datastore

import (
	"encoding/json"
	"fmt"
)

// encodeValue renders a value for the wire with partition attached to every key
// inside it, at any depth.
//
// Value.MarshalJSON cannot do this. A Key carries only what identifies the
// entity — the project, database and namespace come from the client — so the
// public codec has no partition to attach and emits keys without one. That is
// right for a Key travelling inside a program and wrong for one going on the
// wire, which is why every path that sends a value goes through here.
//
// Getting this partial was the bug: until 2026-08-04 only a key that was
// directly a filter's value was partitioned, so `where ref in {refs}` sent keys
// with no project, and so did every keyValue stored as an entity property. A
// downstream reader spotted the filter half by reading the two branches against
// each other and observing that one of them had to be wrong. Following that
// found the property half, which is the one that writes data.
func encodeValue(v Value, partition *wirePartitionID) (json.RawMessage, error) {
	kind, n := v.kind()
	switch {
	case n == 0:
		return nil, ErrEmptyValue
	case n > 1:
		return nil, ErrAmbiguousValue
	}

	var member []byte
	switch kind {
	case KindKey:
		body, err := json.Marshal(v.Key.wire(partition))
		if err != nil {
			return nil, err
		}
		member = append([]byte(`"keyValue":`), body...)

	case KindArray:
		items := make([]json.RawMessage, len(v.Array))
		for i, item := range v.Array {
			encoded, err := encodeValue(item, partition)
			if err != nil {
				return nil, err
			}
			items[i] = encoded
		}
		body, err := json.Marshal(struct {
			Values []json.RawMessage `json:"values"`
		}{Values: items})
		if err != nil {
			return nil, err
		}
		member = append([]byte(`"arrayValue":`), body...)

	case KindEntity:
		if v.Entity.Key != nil {
			return nil, fmt.Errorf("%w: an embedded entity must not carry a key", ErrBadValue)
		}
		properties := make(map[string]json.RawMessage, len(v.Entity.Properties))
		for name, property := range v.Entity.Properties {
			encoded, err := encodeValue(property, partition)
			if err != nil {
				return nil, fmt.Errorf("property %q: %w", name, err)
			}
			properties[name] = encoded
		}
		body, err := json.Marshal(struct {
			Properties map[string]json.RawMessage `json:"properties,omitempty"`
		}{Properties: properties})
		if err != nil {
			return nil, err
		}
		member = append([]byte(`"entityValue":`), body...)

	default:
		// Nothing else can contain a key, so the public codec is already exact.
		return json.Marshal(v)
	}

	out := make([]byte, 0, len(member)+32)
	out = append(out, '{')
	out = append(out, member...)
	if v.ExcludeFromIndexes {
		out = append(out, `,"excludeFromIndexes":true`...)
	}
	return append(out, '}'), nil
}

// encodeProperties renders an entity's properties with partition attached to
// every key inside them.
func encodeProperties(properties map[string]Value, partition *wirePartitionID) (map[string]json.RawMessage, error) {
	if properties == nil {
		return nil, nil
	}
	out := make(map[string]json.RawMessage, len(properties))
	for name, property := range properties {
		encoded, err := encodeValue(property, partition)
		if err != nil {
			return nil, fmt.Errorf("property %q: %w", name, err)
		}
		out[name] = encoded
	}
	return out, nil
}
