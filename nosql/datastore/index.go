package datastore

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Index errors.
var (
	ErrEmptyIndex = errors.New("datastore: index has no kind or no properties")
)

// Direction is the sort order of one indexed property.
type Direction string

// The index directions, spelled as index.yaml spells them.
const (
	Ascending  Direction = "asc"
	Descending Direction = "desc"
)

// IndexProperty is one property of a composite index. Order matters: an index
// serves a query only if its properties appear in the order the query needs.
type IndexProperty struct {
	Name string

	// Direction defaults to Ascending when empty.
	Direction Direction
}

// Index describes a composite index.
//
// Single-property indexes are automatic and need none of this. A composite
// index is required when a query combines an equality filter with an inequality
// on a different property, orders on a property it also filters on
// inequality, or otherwise needs more than one property considered together.
// Without one, the query fails at runtime with FAILED_PRECONDITION on code that
// compiled cleanly, which is the failure this type exists to move earlier.
//
// This is a description, not a request. Applying an index is an admin-API
// operation and requirement:datastore-client-scope excludes the admin API on
// purpose; the shape of an index, though, is a property of this service rather
// than of any one tool, so it belongs here instead of being reinvented by every
// generator and migration script.
//
//	idx := datastore.Index{
//	    Kind: "Task",
//	    Properties: []datastore.IndexProperty{
//	        {Name: "done"},
//	        {Name: "priority", Direction: datastore.Descending},
//	    },
//	}
//	yaml, _ := datastore.MarshalIndexYAML([]datastore.Index{idx})
type Index struct {
	Kind string

	// Ancestor reports whether the index serves ancestor queries.
	Ancestor bool

	Properties []IndexProperty
}

// Valid reports whether the index is complete enough to describe.
func (i Index) Valid() error {
	if i.Kind == "" || len(i.Properties) == 0 {
		return ErrEmptyIndex
	}
	seen := make(map[string]bool, len(i.Properties))
	for _, p := range i.Properties {
		if p.Name == "" {
			return fmt.Errorf("%w: a property has no name", ErrEmptyIndex)
		}
		if p.Direction != "" && p.Direction != Ascending && p.Direction != Descending {
			return fmt.Errorf("datastore: unknown index direction %q", p.Direction)
		}
		if seen[p.Name] {
			return fmt.Errorf("datastore: property %q appears twice in one index", p.Name)
		}
		seen[p.Name] = true
	}
	return nil
}

// String renders the index compactly, for a diagnostic telling an author what
// to create. It is not index.yaml; see MarshalIndexYAML for that.
func (i Index) String() string {
	var b strings.Builder
	b.WriteString(i.Kind)
	if i.Ancestor {
		b.WriteString(" (ancestor)")
	}
	b.WriteString(": ")
	for n, p := range i.Properties {
		if n > 0 {
			b.WriteString(", ")
		}
		b.WriteString(p.Name)
		if p.Direction == Descending {
			b.WriteString(" desc")
		}
	}
	return b.String()
}

// Equal reports whether two indexes describe the same thing. Property order is
// significant; an empty direction equals Ascending.
func (i Index) Equal(other Index) bool {
	if i.Kind != other.Kind || i.Ancestor != other.Ancestor ||
		len(i.Properties) != len(other.Properties) {
		return false
	}
	for n, p := range i.Properties {
		q := other.Properties[n]
		if p.Name != q.Name || p.direction() != q.direction() {
			return false
		}
	}
	return true
}

func (p IndexProperty) direction() Direction {
	if p.Direction == "" {
		return Ascending
	}
	return p.Direction
}

// MarshalIndexYAML renders indexes in the index.yaml form that
// `gcloud datastore indexes create` consumes.
//
// The output is sorted by kind and then by property list, so a tool that
// regenerates it produces a stable diff rather than a reordering.
//
// It is written by hand rather than through a YAML library. The shape is four
// keys deep and closed, and this module has no external dependencies —
// acquiring one for this would cost more than the feature.
func MarshalIndexYAML(indexes []Index) ([]byte, error) {
	for _, i := range indexes {
		if err := i.Valid(); err != nil {
			return nil, err
		}
	}
	sorted := append([]Index(nil), indexes...)
	sort.SliceStable(sorted, func(a, b int) bool {
		if sorted[a].Kind != sorted[b].Kind {
			return sorted[a].Kind < sorted[b].Kind
		}
		return sorted[a].String() < sorted[b].String()
	})

	var b strings.Builder
	b.WriteString("indexes:\n")
	for _, i := range sorted {
		b.WriteString("- kind: ")
		b.WriteString(yamlScalar(i.Kind))
		b.WriteByte('\n')
		b.WriteString("  ancestor: ")
		if i.Ancestor {
			b.WriteString("yes\n")
		} else {
			b.WriteString("no\n")
		}
		b.WriteString("  properties:\n")
		for _, p := range i.Properties {
			b.WriteString("  - name: ")
			b.WriteString(yamlScalar(p.Name))
			b.WriteByte('\n')
			if p.direction() == Descending {
				b.WriteString("    direction: desc\n")
			}
		}
	}
	return []byte(b.String()), nil
}

// yamlScalar quotes a name when a bare scalar would be ambiguous. Property and
// kind names are ordinarily plain identifiers, but __key__ and anything with a
// colon or a leading indicator character are not.
func yamlScalar(s string) string {
	if s == "" {
		return `""`
	}
	plain := true
	for n, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_':
		case r >= '0' && r <= '9' && n > 0:
		case r == '.' && n > 0, r == '-' && n > 0:
		default:
			plain = false
		}
		if !plain {
			break
		}
	}
	if plain {
		return s
	}
	return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
}
