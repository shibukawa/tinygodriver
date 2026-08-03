package datastore

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

type inner struct {
	Note  string `datastore:"note"`
	Count int    `datastore:"count"`
}

type task struct {
	Key        Key       `datastore:"__key__"`
	Title      string    `datastore:"title"`
	Done       bool      `datastore:"done"`
	Priority   int       `datastore:"priority"`
	Ratio      float64   `datastore:"ratio"`
	Payload    []byte    `datastore:"payload"`
	Created    time.Time `datastore:"created"`
	Tags       []string  `datastore:"tags"`
	Meta       inner     `datastore:"meta"`
	Body       string    `datastore:"body,noindex"`
	Optional   string    `datastore:"optional,omitempty"`
	Untagged   int
	Skipped    string `datastore:"-"`
	unexported int    //nolint:unused
}

func TestMarshalEntityRoundTrip(t *testing.T) {
	created := time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)
	original := task{
		Key:      NameKey("Task", "first"),
		Title:    "こんにちは",
		Done:     true,
		Priority: 3,
		Ratio:    0.5,
		Payload:  []byte{0, 1, 255},
		Created:  created,
		Tags:     []string{"a", "b"},
		Meta:     inner{Note: "nested", Count: 7},
		Body:     "long text",
		Untagged: 9,
		Skipped:  "should not appear",
	}

	e, err := MarshalEntity(original)
	if err != nil {
		t.Fatalf("MarshalEntity: %v", err)
	}
	if e.Key == nil || !e.Key.Equal(NameKey("Task", "first")) {
		t.Errorf("key = %v", e.Key)
	}
	if _, present := e.Properties["Skipped"]; present {
		t.Error(`a "-" tag did not skip the field`)
	}
	if _, present := e.Properties["optional"]; present {
		t.Error("omitempty did not skip the zero value")
	}
	if _, present := e.Properties["Untagged"]; !present {
		t.Error("an untagged field should use its Go name")
	}
	if _, present := e.Properties["unexported"]; present {
		t.Error("an unexported field was marshaled")
	}
	if !e.Properties["body"].ExcludeFromIndexes {
		t.Error("noindex did not set ExcludeFromIndexes")
	}
	if e.Properties["title"].ExcludeFromIndexes {
		t.Error("a field without noindex was excluded from indexes")
	}

	var back task
	if err := UnmarshalEntity(e, &back); err != nil {
		t.Fatalf("UnmarshalEntity: %v", err)
	}
	// Skipped never travels, so it cannot come back.
	original.Skipped = ""
	if !reflect.DeepEqual(back, original) {
		t.Errorf("round trip differs\n got %+v\nwant %+v", back, original)
	}
}

// TestMarshalEntityGoesThroughTheWire checks the mapping against the codec
// rather than against itself, so a field that marshals to something the wire
// rejects fails here.
func TestMarshalEntityGoesThroughTheWire(t *testing.T) {
	e, err := MarshalEntity(task{
		Key:     NameKey("Task", "x"),
		Created: time.Unix(1_800_000_000, 0).UTC(),
		Tags:    []string{"a"},
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := e.MarshalJSON()
	if err != nil {
		t.Fatalf("the mapped entity does not encode: %v", err)
	}
	var decoded Entity
	if err := decoded.UnmarshalJSON(raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var back task
	if err := UnmarshalEntity(decoded, &back); err != nil {
		t.Fatalf("UnmarshalEntity after a wire round trip: %v", err)
	}
	if len(back.Tags) != 1 || back.Tags[0] != "a" {
		t.Errorf("tags = %v", back.Tags)
	}
}

// TestIntegerPrecisionSurvivesTheMapper is the property the text encoding
// exists for, checked through reflection rather than only through the
// constructors.
func TestIntegerPrecisionSurvivesTheMapper(t *testing.T) {
	type big struct {
		N int64 `datastore:"n"`
	}
	const beyondFloat64 = int64(9007199254740993)
	e, err := MarshalEntity(big{N: beyondFloat64})
	if err != nil {
		t.Fatal(err)
	}
	if text, _ := e.Properties["n"].AsNumber(); text != "9007199254740993" {
		t.Errorf("stored %q", text)
	}
	var back big
	if err := UnmarshalEntity(e, &back); err != nil {
		t.Fatal(err)
	}
	if back.N != beyondFloat64 {
		t.Errorf("N = %d", back.N)
	}
}

func TestPointerFieldsAndNull(t *testing.T) {
	type withPointer struct {
		S *string `datastore:"s"`
		N *int    `datastore:"n"`
	}
	e, err := MarshalEntity(withPointer{})
	if err != nil {
		t.Fatal(err)
	}
	// A nil pointer becomes null, which is a value, not an absence.
	if !e.Properties["s"].IsNull() || !e.Properties["n"].IsNull() {
		t.Errorf("nil pointers did not become null: %+v", e.Properties)
	}

	s, n := "set", 5
	e, err = MarshalEntity(withPointer{S: &s, N: &n})
	if err != nil {
		t.Fatal(err)
	}
	var back withPointer
	if err := UnmarshalEntity(e, &back); err != nil {
		t.Fatal(err)
	}
	if back.S == nil || *back.S != "set" || back.N == nil || *back.N != 5 {
		t.Errorf("back = %+v", back)
	}
}

func TestNullClearsAndAbsentLeavesAlone(t *testing.T) {
	type pair struct {
		A string `datastore:"a"`
		B string `datastore:"b"`
	}
	out := pair{A: "was", B: "was"}
	e := Entity{Properties: map[string]Value{"a": Null()}}
	if err := UnmarshalEntity(e, &out); err != nil {
		t.Fatal(err)
	}
	if out.A != "" {
		t.Errorf("an explicit null should clear the field, got %q", out.A)
	}
	if out.B != "was" {
		t.Errorf("an absent property should leave the field alone, got %q", out.B)
	}
}

func TestUnknownPropertiesAndMissingFieldsAreNotErrors(t *testing.T) {
	type small struct {
		A string `datastore:"a"`
		B string `datastore:"b"`
	}
	e := Entity{Properties: map[string]Value{
		"a":       String("kept"),
		"unknown": String("ignored"),
	}}
	var out small
	if err := UnmarshalEntity(e, &out); err != nil {
		t.Fatalf("a schemaless entity should decode: %v", err)
	}
	if out.A != "kept" || out.B != "" {
		t.Errorf("out = %+v", out)
	}
}

func TestTypeMismatchIsReported(t *testing.T) {
	type numeric struct {
		N int `datastore:"n"`
	}
	var out numeric
	err := UnmarshalEntity(Entity{Properties: map[string]Value{"n": String("not a number")}}, &out)
	if err == nil {
		t.Fatal("a string decoded into an int field")
	}
	if !strings.Contains(err.Error(), "field N") {
		t.Errorf("error does not name the field: %v", err)
	}
}

func TestIntegerOverflowIsReported(t *testing.T) {
	type small struct {
		N int8 `datastore:"n"`
	}
	var out small
	if err := UnmarshalEntity(Entity{Properties: map[string]Value{"n": Int(1000)}}, &out); err == nil {
		t.Error("1000 was accepted into an int8")
	}
}

func TestUint64BeyondInt64IsRefused(t *testing.T) {
	type u struct {
		N uint64 `datastore:"n"`
	}
	// Datastore integers are signed 64-bit, so this has no representation.
	// Refusing beats wrapping to a negative.
	if _, err := MarshalEntity(u{N: 1 << 63}); err == nil {
		t.Error("a uint64 above MaxInt64 was accepted")
	}
	if _, err := MarshalEntity(u{N: 1<<63 - 1}); err != nil {
		t.Errorf("MaxInt64 should fit: %v", err)
	}
}

func TestMapsAreRefused(t *testing.T) {
	type withMap struct {
		M map[string]string `datastore:"m"`
	}
	_, err := MarshalEntity(withMap{M: map[string]string{"a": "b"}})
	if err == nil {
		t.Fatal("a map was accepted")
	}
	if !strings.Contains(err.Error(), "maps are not supported") {
		t.Errorf("err = %v", err)
	}
}

func TestKeyFieldRules(t *testing.T) {
	type pointerKey struct {
		K *Key   `datastore:"__key__"`
		A string `datastore:"a"`
	}
	k := NameKey("K", "n")
	e, err := MarshalEntity(pointerKey{K: &k})
	if err != nil {
		t.Fatal(err)
	}
	if e.Key == nil || !e.Key.Equal(k) {
		t.Errorf("key = %v", e.Key)
	}
	var back pointerKey
	if err := UnmarshalEntity(e, &back); err != nil {
		t.Fatal(err)
	}
	if back.K == nil || !back.K.Equal(k) {
		t.Errorf("back.K = %v", back.K)
	}

	// A nil *Key marshals to no key at all, which is what an incomplete-key
	// insert wants.
	e, err = MarshalEntity(pointerKey{})
	if err != nil {
		t.Fatal(err)
	}
	if e.Key != nil {
		t.Errorf("a nil key field produced %v", e.Key)
	}

	type wrongKey struct {
		K string `datastore:"__key__"`
	}
	if _, err := MarshalEntity(wrongKey{}); err == nil {
		t.Error("a string __key__ field was accepted")
	}
}

func TestEmbeddedStructMustNotCarryAKey(t *testing.T) {
	type nestedWithKey struct {
		K Key `datastore:"__key__"`
	}
	type outer struct {
		Inner nestedWithKey `datastore:"inner"`
	}
	if _, err := MarshalEntity(outer{}); err == nil {
		t.Error("an embedded struct with a key field was accepted")
	}
}

func TestValueFieldPassesThrough(t *testing.T) {
	type raw struct {
		V Value `datastore:"v"`
	}
	e, err := MarshalEntity(raw{V: GeoPoint(35.6, 139.7)})
	if err != nil {
		t.Fatal(err)
	}
	if e.Properties["v"].Kind() != KindGeoPoint {
		t.Errorf("kind = %v", e.Properties["v"].Kind())
	}
	var back raw
	if err := UnmarshalEntity(e, &back); err != nil {
		t.Fatal(err)
	}
	if point, ok := back.V.AsGeoPoint(); !ok || point.Latitude != 35.6 {
		t.Errorf("back.V = %+v", back.V)
	}
}

func TestMarshalEntityArgumentChecks(t *testing.T) {
	if _, err := MarshalEntity(nil); err == nil {
		t.Error("nil was accepted")
	}
	if _, err := MarshalEntity(42); err == nil {
		t.Error("a non-struct was accepted")
	}
	if _, err := MarshalEntity((*task)(nil)); err == nil {
		t.Error("a nil pointer was accepted")
	}
	var out task
	if err := UnmarshalEntity(Entity{}, out); err == nil {
		t.Error("a non-pointer target was accepted")
	}
	if err := UnmarshalEntity(Entity{}, (*task)(nil)); err == nil {
		t.Error("a nil pointer target was accepted")
	}
}

// TestEntityPassthrough keeps the escape hatch working: a caller already
// holding an Entity should not have to unwrap it.
func TestEntityPassthrough(t *testing.T) {
	e := NewEntity(NameKey("K", "n")).Set("a", String("b"))
	got, err := MarshalEntity(e)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.Properties["a"]; !ok {
		t.Error("the entity was not passed through")
	}
	var out Entity
	if err := UnmarshalEntity(e, &out); err != nil {
		t.Fatal(err)
	}
	if out.Key == nil {
		t.Error("the entity lost its key")
	}
}
