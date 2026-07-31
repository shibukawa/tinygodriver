//go:build !tinygo

package dynamodb_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/shibukawa/tinygodriver/nosql/dynamodb"
)

func TestAttributeValueWireForm(t *testing.T) {
	for _, test := range []struct {
		name string
		av   dynamodb.AttributeValue
		want string
	}{
		{"string", dynamodb.S("u#1"), `{"S":"u#1"}`},
		{"empty string", dynamodb.S(""), `{"S":""}`},
		{"multi-byte", dynamodb.S("こんにちは"), `{"S":"こんにちは"}`},
		{"int", dynamodb.N(42), `{"N":"42"}`},
		{"negative", dynamodb.N(-7), `{"N":"-7"}`},
		{"float", dynamodb.N(1.5), `{"N":"1.5"}`},
		{"uint", dynamodb.N(uint64(18446744073709551615)), `{"N":"18446744073709551615"}`},
		{"precise number", dynamodb.NString("123456789012345678901234567890.5"), `{"N":"123456789012345678901234567890.5"}`},
		{"binary", dynamodb.B([]byte{0, 1, 255}), `{"B":"AAH/"}`},
		{"bool", dynamodb.Bool(true), `{"BOOL":true}`},
		{"null", dynamodb.Null(), `{"NULL":true}`},
		{"empty list", dynamodb.List(), `{"L":[]}`},
		{"list", dynamodb.List(dynamodb.S("a"), dynamodb.Null()), `{"L":[{"S":"a"},{"NULL":true}]}`},
		{"map", dynamodb.Map(map[string]dynamodb.AttributeValue{"k": dynamodb.S("v")}), `{"M":{"k":{"S":"v"}}}`},
		{"string set", dynamodb.SS("a", "b"), `{"SS":["a","b"]}`},
		{"number set", dynamodb.NS("1", "2"), `{"NS":["1","2"]}`},
		{"binary set", dynamodb.BS([]byte{1}, []byte{2}), `{"BS":["AQ==","Ag=="]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := json.Marshal(test.av)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if string(got) != test.want {
				t.Errorf("marshal = %s, want %s", got, test.want)
			}

			var back dynamodb.AttributeValue
			if err := json.Unmarshal(got, &back); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if !reflect.DeepEqual(back, test.av) {
				t.Errorf("round trip = %+v, want %+v", back, test.av)
			}
		})
	}
}

func TestAttributeValueRejectsAmbiguous(t *testing.T) {
	empty := dynamodb.AttributeValue{}
	if _, err := json.Marshal(empty); !errors.Is(err, dynamodb.ErrEmptyAttribute) {
		t.Errorf("empty attribute: err = %v, want ErrEmptyAttribute", err)
	}

	name := "x"
	both := dynamodb.AttributeValue{S: &name, N: &name}
	if _, err := json.Marshal(both); !errors.Is(err, dynamodb.ErrAmbiguousAttribute) {
		t.Errorf("two types set: err = %v, want ErrAmbiguousAttribute", err)
	}
}

func TestAttributeValueRejectsUnknownType(t *testing.T) {
	for _, body := range []string{
		`{"XX":"1"}`,
		`{"S":"a","N":"1"}`,
		`{}`,
		`{"NULL":false}`,
	} {
		var av dynamodb.AttributeValue
		if err := json.Unmarshal([]byte(body), &av); err == nil {
			t.Errorf("unmarshal(%s) succeeded, want an error", body)
		}
	}
}

func TestAttributeValueAccessors(t *testing.T) {
	if v, ok := dynamodb.S("x").AsString(); !ok || v != "x" {
		t.Errorf("AsString = %q, %v", v, ok)
	}
	if _, ok := dynamodb.S("x").AsInt(); ok {
		t.Error("AsInt on a string reported ok")
	}
	if v, ok := dynamodb.N(42).AsInt(); !ok || v != 42 {
		t.Errorf("AsInt = %d, %v", v, ok)
	}
	if _, ok := dynamodb.N(1.5).AsInt(); ok {
		t.Error("AsInt on a fractional number reported ok")
	}
	if v, ok := dynamodb.N(1.5).AsFloat(); !ok || v != 1.5 {
		t.Errorf("AsFloat = %v, %v", v, ok)
	}

	// The reason numbers are text: this one has no float64 representation.
	const precise = "123456789012345678901234567890"
	if v, ok := dynamodb.NString(precise).AsNumber(); !ok || v != precise {
		t.Errorf("AsNumber = %q, %v", v, ok)
	}
	if !dynamodb.Null().IsNull() {
		t.Error("Null().IsNull() = false")
	}
	if dynamodb.Bool(false).IsNull() {
		t.Error("Bool(false) reported as null")
	}
}

type profile struct {
	City string `dynamodbav:"city"`
	Zip  string `dynamodbav:"zip,omitempty"`
}

type user struct {
	PK       string            `dynamodbav:"pk"`
	Age      int               `dynamodbav:"age"`
	Score    float64           `dynamodbav:"score"`
	Active   bool              `dynamodbav:"active"`
	Tags     []string          `dynamodbav:"tags"`
	Blob     []byte            `dynamodbav:"blob"`
	Created  time.Time         `dynamodbav:"created"`
	Profile  profile           `dynamodbav:"profile"`
	Optional *profile          `dynamodbav:"optional"`
	Labels   map[string]string `dynamodbav:"labels"`
	Absent   string            `dynamodbav:"absent,omitempty"`
	Ignored  string            `dynamodbav:"-"`
	unseen   string
}

func TestMarshalItemRoundTrip(t *testing.T) {
	in := user{
		PK: "u#1", Age: 42, Score: 1.5, Active: true,
		Tags:    []string{"a", "b"},
		Blob:    []byte{0, 1, 255},
		Created: time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC),
		Profile: profile{City: "東京"},
		Labels:  map[string]string{"env": "prod"},
		Ignored: "not stored",
		unseen:  "not stored either",
	}

	item, err := dynamodb.MarshalItem(in)
	if err != nil {
		t.Fatalf("MarshalItem: %v", err)
	}
	if _, ok := item["Ignored"]; ok {
		t.Error(`a field tagged "-" was marshalled`)
	}
	if _, ok := item["absent"]; ok {
		t.Error("an empty omitempty field was marshalled")
	}
	if _, ok := item["unseen"]; ok {
		t.Error("an unexported field was marshalled")
	}
	if v, ok := item["pk"].AsString(); !ok || v != "u#1" {
		t.Errorf("pk = %q, %v", v, ok)
	}
	if v, ok := item["age"].AsInt(); !ok || v != 42 {
		t.Errorf("age = %d, %v", v, ok)
	}
	if item["optional"].Kind() != dynamodb.KindNull {
		t.Errorf("a nil pointer became %v, want NULL", item["optional"].Kind())
	}
	if _, ok := item["blob"].AsBytes(); !ok {
		t.Error("blob is not binary")
	}

	var out user
	if err := dynamodb.UnmarshalItem(item, &out); err != nil {
		t.Fatalf("UnmarshalItem: %v", err)
	}
	in.Ignored, in.unseen = "", ""
	if !reflect.DeepEqual(out, in) {
		t.Errorf("round trip =\n %+v\nwant\n %+v", out, in)
	}
}

func TestUnmarshalItemReportsTypeMismatch(t *testing.T) {
	item := dynamodb.Item{"age": dynamodb.S("not a number")}
	var out user
	err := dynamodb.UnmarshalItem(item, &out)
	if err == nil {
		t.Fatal("UnmarshalItem accepted a string for an int field")
	}
	if got := err.Error(); got == "" {
		t.Error("error has no message")
	}
}

func TestMarshalItemWantsAStruct(t *testing.T) {
	if _, err := dynamodb.MarshalItem(42); err == nil {
		t.Error("MarshalItem(42) succeeded")
	}
	if _, err := dynamodb.MarshalItem((*user)(nil)); err == nil {
		t.Error("MarshalItem(nil pointer) succeeded")
	}

	// An item passes through untouched, which is what lets a caller mix the
	// two styles.
	item := dynamodb.Item{"pk": dynamodb.S("x")}
	got, err := dynamodb.MarshalItem(item)
	if err != nil || !reflect.DeepEqual(got, item) {
		t.Errorf("MarshalItem(Item) = %v, %v", got, err)
	}
}
