package datastore

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
)

// referenceUnmarshalValue is the wholly map-based decode the scanning fast
// path replaced, kept as the oracle the fast path is compared against.
func referenceUnmarshalValue(b []byte) (Value, error) {
	var members map[string]json.RawMessage
	if err := json.Unmarshal(b, &members); err != nil {
		return Value{}, fmt.Errorf("%w: %s", ErrBadValue, err)
	}
	out := Value{}
	if raw, ok := members["excludeFromIndexes"]; ok {
		if err := json.Unmarshal(raw, &out.ExcludeFromIndexes); err != nil {
			return Value{}, fmt.Errorf("%w: excludeFromIndexes", ErrBadValue)
		}
	}
	name, count := "", 0
	for key := range members {
		if nonUnionMembers[key] {
			continue
		}
		name, count = key, count+1
	}
	switch {
	case count == 0:
		return Value{}, ErrEmptyValue
	case count > 1:
		return Value{}, ErrAmbiguousValue
	}
	var probe Value
	object := []byte(`{"` + name + `":` + string(members[name]) + `}`)
	if err := probe.UnmarshalJSON(object); err != nil {
		return Value{}, err
	}
	probe.ExcludeFromIndexes = out.ExcludeFromIndexes
	return probe, nil
}

func TestValueUnmarshalJSONMatchesReference(t *testing.T) {
	inputs := []string{
		`{"stringValue":"room-1"}`,
		`{"stringValue":""}`,
		`{"stringValue":"quote \" here"}`,
		`{"integerValue":"1785432762"}`,
		`{"doubleValue":21.5}`,
		`{"booleanValue":true}`,
		`{"nullValue":null}`,
		`{"nullValue":0}`,
		`{"timestampValue":"2026-07-31T12:00:00Z"}`,
		`{"blobValue":"aGVsbG8="}`,
		`{"geoPointValue":{"latitude":35.0,"longitude":139.0}}`,
		`{"keyValue":{"path":[{"kind":"Task","id":"42"}]}}`,
		`{"entityValue":{"properties":{"a":{"stringValue":"x"}}}}`,
		`{"arrayValue":{"values":[{"integerValue":"1"},{"stringValue":"two"}]}}`,
		`{"arrayValue":{"values":[]}}`,
		`{"arrayValue":{}}`,
		`{"stringValue":"x","excludeFromIndexes":true}`,
		`{"excludeFromIndexes":true,"stringValue":"x"}`,
		`{"stringValue":"x","excludeFromIndexes":false}`,
		`{"stringValue":"x","meaning":9}`,
		`{"meaning":9,"excludeFromIndexes":true,"integerValue":"7"}`,
		"  {\n\t\"stringValue\" : \"spaced\"\n}  ",
		`{"stringValue":"a","integerValue":"1"}`,
		`{"excludeFromIndexes":true}`,
		`{}`,
		`{"unknownValue":"x"}`,
		`{"excludeFromIndexes":"yes","stringValue":"x"}`,
		`{"stringValue":"a","stringValue":"b"}`,
		`{"integerValue":"not a number"}`,
		`{"integerValue":42}`,
		`{"doubleValue":"21.5"}`,
		`{"blobValue":"!!!"}`,
		`{"timestampValue":"yesterday"}`,
		`{"stringValue":}`,
		`{"stringValue":"a"`,
		`{"stringValue":"a"}}`,
		`{"stringValue":"}"}`,
		`null`,
		`[]`,
		``,
	}
	for _, input := range inputs {
		var got Value
		gotErr := got.UnmarshalJSON([]byte(input))
		want, wantErr := referenceUnmarshalValue([]byte(input))
		if (gotErr == nil) != (wantErr == nil) {
			t.Errorf("%s: error %v, reference error %v", input, gotErr, wantErr)
			continue
		}
		if gotErr != nil {
			for _, sentinel := range []error{ErrEmptyValue, ErrAmbiguousValue, ErrBadValue} {
				if errors.Is(gotErr, sentinel) != errors.Is(wantErr, sentinel) {
					t.Errorf("%s: error %v, reference error %v disagree on %v",
						input, gotErr, wantErr, sentinel)
				}
			}
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: got %+v, reference %+v", input, got, want)
		}
	}
}
