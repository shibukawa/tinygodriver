package datastore

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestValueMarshalsOneMember(t *testing.T) {
	when := time.Date(2026, 8, 2, 12, 0, 0, 123456000, time.UTC)
	cases := []struct {
		name  string
		value Value
		want  string
	}{
		{"string", String("hello"), `{"stringValue":"hello"}`},
		{"empty string", String(""), `{"stringValue":""}`},
		{"multibyte", String("こんにちは"), `{"stringValue":"こんにちは"}`},
		{"int", Int(42), `{"integerValue":"42"}`},
		{"int is text", Int(int64(9007199254740993)), `{"integerValue":"9007199254740993"}`},
		{"int string", IntString("-9223372036854775808"), `{"integerValue":"-9223372036854775808"}`},
		{"double", Float(1.5), `{"doubleValue":1.5}`},
		{"bool", Bool(true), `{"booleanValue":true}`},
		{"null", Null(), `{"nullValue":null}`},
		{"timestamp", Time(when), `{"timestampValue":"2026-08-02T12:00:00.123456Z"}`},
		{"blob", Blob([]byte{0, 1, 2}), `{"blobValue":"AAEC"}`},
		{"empty blob", Blob(nil), `{"blobValue":""}`},
		{"geo", GeoPoint(35.6, 139.7), `{"geoPointValue":{"latitude":35.6,"longitude":139.7}}`},
		{"array", Array(String("a"), Int(1)), `{"arrayValue":{"values":[{"stringValue":"a"},{"integerValue":"1"}]}}`},
		{"empty array", Array(), `{"arrayValue":{"values":[]}}`},
		{"unindexed", Unindexed(String("x")), `{"stringValue":"x","excludeFromIndexes":true}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := json.Marshal(c.value)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if string(got) != c.want {
				t.Errorf("got  %s\nwant %s", got, c.want)
			}
		})
	}
}

// TestIntegerStaysTextThroughRoundTrip is the property proto3 JSON exists to
// give: an int64 beyond float64's exact range must survive.
func TestIntegerStaysTextThroughRoundTrip(t *testing.T) {
	const beyondFloat64 = int64(9007199254740993) // 2^53 + 1
	raw, err := json.Marshal(Int(beyondFloat64))
	if err != nil {
		t.Fatal(err)
	}
	var back Value
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	got, ok := back.AsInt()
	if !ok || got != beyondFloat64 {
		t.Errorf("AsInt = %d, %v; want %d", got, ok, beyondFloat64)
	}
	if text, _ := back.AsNumber(); text != "9007199254740993" {
		t.Errorf("AsNumber = %q", text)
	}
}

func TestValueRejectsZeroAndAmbiguous(t *testing.T) {
	if _, err := json.Marshal(Value{}); err == nil || !strings.Contains(err.Error(), ErrEmptyValue.Error()) {
		t.Errorf("zero value err = %v, want ErrEmptyValue", err)
	}
	s, n := "x", "1"
	both := Value{String: &s, Integer: &n}
	if _, err := json.Marshal(both); err == nil || !strings.Contains(err.Error(), ErrAmbiguousValue.Error()) {
		t.Errorf("two members err = %v, want ErrAmbiguousValue", err)
	}
}

func TestValueUnmarshalRejectsZeroAndAmbiguous(t *testing.T) {
	var v Value
	if err := json.Unmarshal([]byte(`{}`), &v); err != ErrEmptyValue {
		t.Errorf("empty object err = %v", err)
	}
	if err := json.Unmarshal([]byte(`{"stringValue":"a","integerValue":"1"}`), &v); err != ErrAmbiguousValue {
		t.Errorf("two members err = %v", err)
	}
}

// TestNullIsNotAbsent pins the distinction a filter depends on.
func TestNullIsNotAbsent(t *testing.T) {
	e := NewEntity(NameKey("K", "n")).Set("explicit", Null())
	if v, ok := e.Get("explicit"); !ok || !v.IsNull() {
		t.Error("explicit null was not stored as null")
	}
	if _, ok := e.Get("absent"); ok {
		t.Error("absent property reported present")
	}
}

// TestIntegerAndDoubleDoNotConvert pins that the two number types stay apart.
// Collapsing them would change sort order and equality filters.
func TestIntegerAndDoubleDoNotConvert(t *testing.T) {
	if _, ok := Int(1).AsFloat(); ok {
		t.Error("AsFloat accepted an integerValue")
	}
	if _, ok := Float(1).AsInt(); ok {
		t.Error("AsInt accepted a doubleValue")
	}
}

func TestValueUnmarshalsWire(t *testing.T) {
	raw := `{
		"stringValue": "s",
		"excludeFromIndexes": true
	}`
	var v Value
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatal(err)
	}
	if s, _ := v.AsString(); s != "s" {
		t.Errorf("AsString = %q", s)
	}
	if !v.ExcludeFromIndexes {
		t.Error("excludeFromIndexes lost")
	}
	if v.Kind() != KindString {
		t.Errorf("Kind = %v", v.Kind())
	}
}

func TestNestedEntityMustNotCarryKey(t *testing.T) {
	withKey := Nested(NewEntity(NameKey("K", "n")))
	if _, err := json.Marshal(withKey); err == nil {
		t.Error("an embedded entity with a key was accepted")
	}
	ok := Nested(Entity{Properties: map[string]Value{"a": String("b")}})
	if _, err := json.Marshal(ok); err != nil {
		t.Errorf("keyless embedded entity rejected: %v", err)
	}
}

func TestUnparseableIntegerIsCaughtLocally(t *testing.T) {
	if _, err := json.Marshal(IntString("not a number")); err == nil {
		t.Error("a non-numeric integerValue was accepted")
	}
}

func TestNestedRoundTrip(t *testing.T) {
	when := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	original := Array(
		String("top"),
		Nested(Entity{Properties: map[string]Value{
			"inner": Array(Int(1), Blob([]byte("bytes")), Null()),
			"when":  Time(when),
		}}),
	)
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var back Value
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	items, ok := back.AsArray()
	if !ok || len(items) != 2 {
		t.Fatalf("array = %v, %v", items, ok)
	}
	nested, ok := items[1].AsEntity()
	if !ok {
		t.Fatal("second item is not an entity")
	}
	inner, _ := nested.Get("inner")
	values, _ := inner.AsArray()
	if len(values) != 3 {
		t.Fatalf("inner array length %d", len(values))
	}
	if b, _ := values[1].AsBytes(); string(b) != "bytes" {
		t.Errorf("blob round trip = %q", b)
	}
	if !values[2].IsNull() {
		t.Error("null lost in the round trip")
	}
	stamp, _ := nested.Get("when")
	if got, _ := stamp.AsTime(); !got.Equal(when) {
		t.Errorf("timestamp = %v, want %v", got, when)
	}
}
