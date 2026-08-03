package datastore

import (
	"errors"
	"strings"
	"testing"
)

func TestMarshalIndexYAML(t *testing.T) {
	got, err := MarshalIndexYAML([]Index{
		{
			Kind: "Task",
			Properties: []IndexProperty{
				{Name: "done"},
				{Name: "priority", Direction: Descending},
			},
		},
		{
			Kind:     "Task",
			Ancestor: true,
			Properties: []IndexProperty{
				{Name: "created", Direction: Ascending},
			},
		},
	})
	if err != nil {
		t.Fatalf("MarshalIndexYAML: %v", err)
	}
	want := `indexes:
- kind: Task
  ancestor: yes
  properties:
  - name: created
- kind: Task
  ancestor: no
  properties:
  - name: done
  - name: priority
    direction: desc
`
	if string(got) != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// TestMarshalIndexYAMLIsStable is the property a regenerating tool depends on:
// the same set in a different order produces the same bytes, so a diff shows
// real changes rather than reordering.
func TestMarshalIndexYAMLIsStable(t *testing.T) {
	a := Index{Kind: "B", Properties: []IndexProperty{{Name: "x"}}}
	b := Index{Kind: "A", Properties: []IndexProperty{{Name: "y"}}}

	first, err := MarshalIndexYAML([]Index{a, b})
	if err != nil {
		t.Fatal(err)
	}
	second, err := MarshalIndexYAML([]Index{b, a})
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Errorf("input order changed the output:\n%s\n---\n%s", first, second)
	}
	if !strings.HasPrefix(string(first), "indexes:\n- kind: A\n") {
		t.Errorf("not sorted by kind:\n%s", first)
	}
}

func TestIndexRejectsIncompleteInput(t *testing.T) {
	cases := []struct {
		name  string
		index Index
	}{
		{"no kind", Index{Properties: []IndexProperty{{Name: "a"}}}},
		{"no properties", Index{Kind: "K"}},
		{"unnamed property", Index{Kind: "K", Properties: []IndexProperty{{}}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := c.index.Valid(); !errors.Is(err, ErrEmptyIndex) {
				t.Errorf("Valid = %v, want ErrEmptyIndex", err)
			}
			if _, err := MarshalIndexYAML([]Index{c.index}); err == nil {
				t.Error("MarshalIndexYAML accepted it")
			}
		})
	}
}

func TestIndexRejectsBadDirectionAndDuplicates(t *testing.T) {
	bad := Index{Kind: "K", Properties: []IndexProperty{{Name: "a", Direction: "sideways"}}}
	if err := bad.Valid(); err == nil {
		t.Error("unknown direction accepted")
	}
	dup := Index{Kind: "K", Properties: []IndexProperty{{Name: "a"}, {Name: "a"}}}
	if err := dup.Valid(); err == nil {
		t.Error("duplicate property accepted")
	}
}

func TestIndexEqualTreatsEmptyDirectionAsAscending(t *testing.T) {
	implicit := Index{Kind: "K", Properties: []IndexProperty{{Name: "a"}}}
	explicit := Index{Kind: "K", Properties: []IndexProperty{{Name: "a", Direction: Ascending}}}
	if !implicit.Equal(explicit) {
		t.Error("an empty direction should equal Ascending")
	}

	// Property order is significant: an index on (a, b) does not serve (b, a).
	ab := Index{Kind: "K", Properties: []IndexProperty{{Name: "a"}, {Name: "b"}}}
	ba := Index{Kind: "K", Properties: []IndexProperty{{Name: "b"}, {Name: "a"}}}
	if ab.Equal(ba) {
		t.Error("property order must be significant")
	}

	ancestor := ab
	ancestor.Ancestor = true
	if ab.Equal(ancestor) {
		t.Error("the ancestor flag must be significant")
	}
}

func TestIndexYAMLQuotesAmbiguousNames(t *testing.T) {
	got, err := MarshalIndexYAML([]Index{{
		Kind:       "Task",
		Properties: []IndexProperty{{Name: "__key__"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	// A leading underscore is fine bare, but a name that starts with a
	// character YAML reads as an indicator must be quoted. __key__ is plain.
	if !strings.Contains(string(got), "- name: __key__") {
		t.Errorf("got:\n%s", got)
	}

	got, err = MarshalIndexYAML([]Index{{
		Kind:       "Odd Kind",
		Properties: []IndexProperty{{Name: "a:b"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), `- kind: "Odd Kind"`) || !strings.Contains(string(got), `- name: "a:b"`) {
		t.Errorf("ambiguous scalars were not quoted:\n%s", got)
	}
}

func TestIndexString(t *testing.T) {
	i := Index{
		Kind:     "Task",
		Ancestor: true,
		Properties: []IndexProperty{
			{Name: "done"},
			{Name: "priority", Direction: Descending},
		},
	}
	if got, want := i.String(), "Task (ancestor): done, priority desc"; got != want {
		t.Errorf("String = %q, want %q", got, want)
	}
}
