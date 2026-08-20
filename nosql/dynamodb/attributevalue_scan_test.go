package dynamodb

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
)

// referenceUnmarshal is the map-based decode the scanner fast path replaced,
// kept as the oracle: for every input the two must agree on the value and on
// whether they fail.
func referenceUnmarshal(data []byte) (AttributeValue, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return AttributeValue{}, err
	}
	if len(raw) != 1 {
		return AttributeValue{}, fmt.Errorf("dynamodb: attribute value has %d members, want 1", len(raw))
	}
	var a AttributeValue
	for name, value := range raw {
		if err := a.unmarshalMember([]byte(name), value); err != nil {
			return AttributeValue{}, err
		}
	}
	return a, nil
}

func TestUnmarshalJSONMatchesReference(t *testing.T) {
	inputs := []string{
		`{"S":"room-1"}`,
		`{"S":""}`,
		`{"S":"quote \" and \\ escape"}`,
		`{"N":"1785432762"}`,
		`{"N":"21.5"}`,
		`{"B":"aGVsbG8="}`,
		`{"BOOL":true}`,
		`{"BOOL":false}`,
		`{"NULL":true}`,
		`{"NULL":false}`,
		`{"L":[{"S":"a"},{"N":"1"}]}`,
		`{"L":[]}`,
		`{"M":{"k":{"S":"v"},"n":{"N":"2"}}}`,
		`{"M":{}}`,
		`{"SS":["a","b"]}`,
		`{"NS":["1","2"]}`,
		`{"BS":["aGk="]}`,
		"  {\n\t\"S\" : \"spaced\"\n}  ",
		`{"S":"a","N":"1"}`,
		`{"S":"a","S":"b"}`,
		`{}`,
		`{"Unknown":"x"}`,
		`{"SS":"escaped name"}`,
		`{"S":}`,
		`{"S":"a"`,
		`{"S":"a"}}`,
		`{"S":"a"}{"S":"b"}`,
		`{"L":[1,2}}`,
		`{"M":{"a":"}"}}`,
		`{"S":"}"}`,
		`{"N":1785432762}`,
		`{"NULL":null}`,
		`null`,
		`[]`,
		`"S"`,
		``,
		`   `,
	}
	for _, input := range inputs {
		var got AttributeValue
		gotErr := got.UnmarshalJSON([]byte(input))
		want, wantErr := referenceUnmarshal([]byte(input))
		if (gotErr == nil) != (wantErr == nil) {
			t.Errorf("%s: error %v, reference error %v", input, gotErr, wantErr)
			continue
		}
		if gotErr != nil {
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: got %+v, reference %+v", input, got, want)
		}
	}
}
