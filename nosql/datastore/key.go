package datastore

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Key errors.
var (
	ErrEmptyKeyPath  = errors.New("datastore: key has no path elements")
	ErrAmbiguousID   = errors.New("datastore: path element has both an id and a name")
	ErrIncompleteKey = errors.New("datastore: key is incomplete")
)

// PathElement is one step of a key path: a kind, plus either a numeric id or a
// string name. Neither set means an incomplete key, which the server completes
// on insert.
type PathElement struct {
	Kind string
	ID   int64
	Name string
}

// Incomplete reports whether the server still has to allocate an identifier.
func (p PathElement) Incomplete() bool { return p.ID == 0 && p.Name == "" }

// Key identifies an entity. The last path element is the entity itself; the
// ones before it are its ancestors, which is why ancestry is part of identity
// here rather than a property.
//
// Namespace is a tenancy dimension with no DynamoDB equivalent. It is empty for
// the default namespace.
//
// The project and database are not carried here. A Client adds them at encode
// time, so a Key stays portable inside a program.
type Key struct {
	Namespace string
	Path      []PathElement
}

// NameKey builds a one-element key with a string name.
func NameKey(kind, name string) Key {
	return Key{Path: []PathElement{{Kind: kind, Name: name}}}
}

// IDKey builds a one-element key with a numeric id.
func IDKey(kind string, id int64) Key {
	return Key{Path: []PathElement{{Kind: kind, ID: id}}}
}

// IncompleteKey builds a one-element key for the server to complete.
func IncompleteKey(kind string) Key {
	return Key{Path: []PathElement{{Kind: kind}}}
}

// Child returns k with one more path element appended, making k its ancestor.
func (k Key) Child(e PathElement) Key {
	path := make([]PathElement, 0, len(k.Path)+1)
	path = append(path, k.Path...)
	path = append(path, e)
	return Key{Namespace: k.Namespace, Path: path}
}

// WithNamespace returns k in the given namespace.
func (k Key) WithNamespace(namespace string) Key {
	k.Namespace = namespace
	return k
}

// Kind is the kind of the last path element, which is the entity's own kind.
func (k Key) Kind() string {
	if len(k.Path) == 0 {
		return ""
	}
	return k.Path[len(k.Path)-1].Kind
}

// Incomplete reports whether the last element still needs an identifier.
func (k Key) Incomplete() bool {
	if len(k.Path) == 0 {
		return true
	}
	return k.Path[len(k.Path)-1].Incomplete()
}

// Valid reports whether k can be sent. An incomplete key is valid; only insert
// and AllocateIDs accept one, which is checked where it matters.
func (k Key) Valid() error {
	if len(k.Path) == 0 {
		return ErrEmptyKeyPath
	}
	for _, e := range k.Path {
		if e.Kind == "" {
			return fmt.Errorf("%w: path element has no kind", ErrEmptyKeyPath)
		}
		if e.ID != 0 && e.Name != "" {
			return ErrAmbiguousID
		}
	}
	// Only the last element may be incomplete: an ancestor without an
	// identifier does not name anything.
	for _, e := range k.Path[:len(k.Path)-1] {
		if e.Incomplete() {
			return fmt.Errorf("%w: an ancestor has no identifier", ErrIncompleteKey)
		}
	}
	return nil
}

// Equal reports whether two keys name the same entity.
func (k Key) Equal(other Key) bool {
	if k.Namespace != other.Namespace || len(k.Path) != len(other.Path) {
		return false
	}
	for i, e := range k.Path {
		if e != other.Path[i] {
			return false
		}
	}
	return true
}

// String renders the key for logs and error messages. It is not a wire format
// and nothing parses it back.
func (k Key) String() string {
	var b strings.Builder
	if k.Namespace != "" {
		b.WriteString(k.Namespace)
		b.WriteByte('/')
	}
	for i, e := range k.Path {
		if i > 0 {
			b.WriteByte('/')
		}
		b.WriteString(e.Kind)
		switch {
		case e.Name != "":
			b.WriteByte(',')
			b.WriteString(strconv.Quote(e.Name))
		case e.ID != 0:
			b.WriteByte(',')
			b.WriteString(strconv.FormatInt(e.ID, 10))
		default:
			b.WriteString(",?")
		}
	}
	return b.String()
}

// wirePartitionID is the project, database and namespace a key belongs to.
type wirePartitionID struct {
	ProjectID   string `json:"projectId,omitempty"`
	DatabaseID  string `json:"databaseId,omitempty"`
	NamespaceID string `json:"namespaceId,omitempty"`
}

type wirePathElement struct {
	Kind string `json:"kind"`
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
}

type wireKey struct {
	PartitionID *wirePartitionID  `json:"partitionId,omitempty"`
	Path        []wirePathElement `json:"path"`
}

// MarshalJSON emits the key without a project. A Client fills the partition in
// on the way out, because only it knows which project and database the request
// is for; see (*Client).encodeKey.
func (k Key) MarshalJSON() ([]byte, error) {
	return json.Marshal(k.wire(nil))
}

func (k Key) wire(partition *wirePartitionID) wireKey {
	path := make([]wirePathElement, len(k.Path))
	for i, e := range k.Path {
		path[i] = wirePathElement{Kind: e.Kind, Name: e.Name}
		if e.ID != 0 {
			// int64 is text on the wire, the same proto3 JSON rule that makes
			// integerValue a string.
			path[i].ID = strconv.FormatInt(e.ID, 10)
		}
	}
	if partition != nil && k.Namespace != "" {
		withNamespace := *partition
		withNamespace.NamespaceID = k.Namespace
		partition = &withNamespace
	}
	return wireKey{PartitionID: partition, Path: path}
}

// UnmarshalJSON reads a key, keeping only the namespace from the partition: the
// project and database are the client's, not the key's.
func (k *Key) UnmarshalJSON(b []byte) error {
	var w wireKey
	if err := json.Unmarshal(b, &w); err != nil {
		return fmt.Errorf("datastore: malformed key: %w", err)
	}
	*k = Key{Path: make([]PathElement, len(w.Path))}
	if w.PartitionID != nil {
		k.Namespace = w.PartitionID.NamespaceID
	}
	for i, e := range w.Path {
		k.Path[i] = PathElement{Kind: e.Kind, Name: e.Name}
		if e.ID != "" {
			id, err := strconv.ParseInt(e.ID, 10, 64)
			if err != nil {
				return fmt.Errorf("datastore: malformed key id %q", e.ID)
			}
			k.Path[i].ID = id
		}
	}
	return nil
}
