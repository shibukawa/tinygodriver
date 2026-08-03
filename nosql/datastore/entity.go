package datastore

import "encoding/json"

// Entity is a key and its properties. Datastore is schemaless, so two entities
// of one kind need not carry the same properties.
//
// Version and UpdateTime come back from a read and feed the write preconditions
// in WithBaseVersion and WithUpdateTime. They are ignored on the way out.
type Entity struct {
	Key        *Key
	Properties map[string]Value

	Version    int64
	UpdateTime string
	CreateTime string
}

// NewEntity builds an entity with a key and no properties.
func NewEntity(key Key) Entity {
	return Entity{Key: &key, Properties: map[string]Value{}}
}

// Set stores a property, allocating the map on first use, and returns e so
// calls chain.
func (e Entity) Set(name string, v Value) Entity {
	if e.Properties == nil {
		e.Properties = map[string]Value{}
	}
	e.Properties[name] = v
	return e
}

// Get returns a property. The second result distinguishes an absent property
// from one explicitly set to null, which are different things to a filter.
func (e Entity) Get(name string) (Value, bool) {
	v, ok := e.Properties[name]
	return v, ok
}

// wireEntity is the JSON shape. Version, createTime and updateTime live on
// EntityResult rather than here, so they are not part of this struct's tags.
type wireEntity struct {
	Key        *json.RawMessage `json:"key,omitempty"`
	Properties map[string]Value `json:"properties,omitempty"`
}

// MarshalJSON emits the entity without a partition on its key; a Client fills
// that in on the way out.
func (e Entity) MarshalJSON() ([]byte, error) {
	var out wireEntity
	if e.Key != nil {
		raw, err := json.Marshal(e.Key)
		if err != nil {
			return nil, err
		}
		msg := json.RawMessage(raw)
		out.Key = &msg
	}
	out.Properties = e.Properties
	return json.Marshal(out)
}

// UnmarshalJSON reads an entity.
func (e *Entity) UnmarshalJSON(b []byte) error {
	var w struct {
		Key        *Key             `json:"key"`
		Properties map[string]Value `json:"properties"`
	}
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	e.Key = w.Key
	e.Properties = w.Properties
	if e.Properties == nil {
		e.Properties = map[string]Value{}
	}
	return nil
}

// wireEntityResult is an entity plus the metadata a read returns alongside it.
type wireEntityResult struct {
	Entity     Entity `json:"entity"`
	Version    string `json:"version"`
	CreateTime string `json:"createTime"`
	UpdateTime string `json:"updateTime"`
	Cursor     string `json:"cursor"`
}

func (r wireEntityResult) entity() Entity {
	e := r.Entity
	e.Version = parseInt64(r.Version)
	e.CreateTime = r.CreateTime
	e.UpdateTime = r.UpdateTime
	return e
}

// LookupResult is what a batch read returns. All three lists matter: the server
// answers a lookup by partitioning the keys rather than by failing, and a
// caller batching a thousand keys needs to know which came back.
type LookupResult struct {
	Found []Entity

	// Missing are keys with no entity.
	Missing []Key

	// Deferred are keys the server did not read this time. They are handed back
	// rather than retried inside the call, because that is the caller's
	// decision about which reads still matter.
	Deferred []Key
}

// HasDeferred reports whether the server left keys unread.
func (r *LookupResult) HasDeferred() bool { return r != nil && len(r.Deferred) > 0 }
