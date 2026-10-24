package authn

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"strings"
	"testing"
)

// referenceValidateJSON is the validator as originally written, over
// encoding/json's token stream. It stays here as the oracle the scanner is
// checked against: every document the two disagree on is a bug in one of them.
func referenceValidateJSON(data []byte, options JSONOptions) error {
	if options.MaxBytes <= 0 || options.MaxDepth <= 0 || options.MaxMembers <= 0 {
		return ErrInvalidSize
	}
	if len(data) > options.MaxBytes {
		return ErrLimitExceeded
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	members := 0
	if err := referenceValue(decoder, 1, options, &members); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("%w: trailing value", ErrMalformedJSON)
		}
		return fmt.Errorf("%w: %v", ErrMalformedJSON, err)
	}
	return nil
}

func referenceValue(decoder *json.Decoder, depth int, options JSONOptions, members *int) error {
	if depth > options.MaxDepth {
		return ErrLimitExceeded
	}
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrMalformedJSON, err)
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return fmt.Errorf("%w: %v", ErrMalformedJSON, err)
			}
			key, ok := keyToken.(string)
			if !ok {
				return ErrMalformedJSON
			}
			(*members)++
			if *members > options.MaxMembers {
				return ErrLimitExceeded
			}
			if _, duplicate := seen[key]; duplicate {
				return ErrDuplicateJSON
			}
			seen[key] = struct{}{}
			if err := referenceValue(decoder, depth+1, options, members); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			(*members)++
			if *members > options.MaxMembers {
				return ErrLimitExceeded
			}
			if err := referenceValue(decoder, depth+1, options, members); err != nil {
				return err
			}
		}
	default:
		return ErrMalformedJSON
	}
	closing, err := decoder.Token()
	if err != nil {
		return ErrMalformedJSON
	}
	want := json.Delim(']')
	if delimiter == '{' {
		want = json.Delim('}')
	}
	if closing != want {
		return ErrMalformedJSON
	}
	return nil
}

// verdict reduces an error to what the callers distinguish: accepted, over a
// limit, a duplicate name, or malformed.
func verdict(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, ErrLimitExceeded):
		return "limit"
	case errors.Is(err, ErrDuplicateJSON):
		return "duplicate"
	case errors.Is(err, ErrMalformedJSON):
		return "malformed"
	}
	return err.Error()
}

var jsonCases = []string{
	`{}`, `[]`, `{"a":1}`, `{"a":1,"b":2}`, `{"a":{"b":{"c":[1,2,3]}}}`,
	`[1,2,3]`, `[[[[]]]]`, `"s"`, `1`, `-0`, `0`, `1.5`, `1e5`, `1E+5`, `-1.5e-5`, `true`, `false`, `null`,
	` {"a" : 1 } `, "\t{\n\"a\"\r:\n1}\n",
	`{"a":1,"a":2}`, `{"a":{"b":1,"b":2}}`, `{"a":1,"b":{"a":2}}`, `{"\u0061":1,"a":2}`, `{"a\u0062":1,"ab":2}`,
	`{"\ud83d\ude00":1,"😀":2}`, `{"\ud83d":1,"\ufffd":2}`, `{"\udc00":1,"\ufffd":2}`, "{\"\xff\":1,\"\ufffd\":2}", "{\"\xff\":1,\"\xfe\":2}",
	`{"a":"\"\\\/\b\f\n\r\t\u00e9"}`, `{"a":"\x"}`, `{"a":"\u00"}`, `{"a":"\u00g0"}`, "{\"a\":\"\x01\"}", "{\"a\":\"\x1f\"}", `{"a":"` + "\x7f" + `"}`,
	`{"a":1,}`, `{,"a":1}`, `{"a"}`, `{"a":}`, `{"a":1 "b":2}`, `{1:2}`, `{"a":1]`, `[1,2,]`, `[,1]`, `[1 2]`, `[1,2}`,
	`01`, `1.`, `.5`, `+1`, `1e`, `1e+`, `--1`, `1x`, `truex`, `tru`, `nul`, `True`, `NULL`, `""`, `"`, `"abc`, ``, ` `, `{`, `}`, `[`, `]`,
	`{"a":1}{"b":2}`, `{"a":1} x`, `{"a":1} 2`, `[1][2]`, `1 2`, `"a" "b"`,
	`{"a":[1,{"b":[2,{"c":3}]}]}`, `{"":1}`, `{"":1,"":2}`, `[{"a":1},{"a":1}]`, `{"a":[],"b":{}}`,
	"{\"a\":1}\x00", "\xef\xbb\xbf{}", `{"a":1}` + "\u00a0", `{"a":"\u0000"}`, `{"a":"\/"}`,
	`{"alg":"HS256","typ":"JWT","kid":"k"}`, `{"iss":"https://issuer.example","sub":"device-42","aud":["https://api.example"],"exp":4102444800}`,
	`[1e309]`, `[-]`, `[-a]`, `{"a":-}`, `{"a":1e5.5}`, `{"a":0.0e0}`, `{"a":00}`, `{"a":"\uD834\uDD1E"}`, `{"a":"\uDD1E\uD834"}`,
}

func TestValidateJSONMatchesReference(t *testing.T) {
	options := JSONOptions{MaxBytes: 4096, MaxDepth: 16, MaxMembers: 256}
	for _, c := range jsonCases {
		compareValidate(t, []byte(c), options)
	}
	// Every limit, at and just past its edge.
	deep := strings.Repeat("[", 20) + strings.Repeat("]", 20)
	deepObject := strings.Repeat(`{"a":`, 20) + "1" + strings.Repeat("}", 20)
	wide := "[" + strings.TrimSuffix(strings.Repeat("1,", 30), ",") + "]"
	wideObject := "{"
	for i := range 30 {
		if i > 0 {
			wideObject += ","
		}
		wideObject += fmt.Sprintf(`"k%d":%d`, i, i)
	}
	wideObject += "}"
	for depth := 1; depth <= 22; depth++ {
		for members := 1; members <= 32; members++ {
			o := JSONOptions{MaxBytes: 4096, MaxDepth: depth, MaxMembers: members}
			for _, c := range []string{deep, deepObject, wide, wideObject, `{"a":[{"b":[1,2]},3]}`, `1`} {
				compareValidate(t, []byte(c), o)
			}
		}
	}
	compareValidate(t, []byte(`{"a":1}`), JSONOptions{MaxBytes: 6, MaxDepth: 2, MaxMembers: 1})
	compareValidate(t, []byte(`{"a":1}`), JSONOptions{MaxBytes: 7, MaxDepth: 2, MaxMembers: 1})
	for _, o := range []JSONOptions{{}, {MaxBytes: 1}, {MaxBytes: 1, MaxDepth: 1}, {MaxDepth: 1, MaxMembers: 1}} {
		compareValidate(t, []byte(`1`), o)
	}

	// Objects past the scanned-name bound, with and without a late duplicate.
	for _, dup := range []bool{false, true} {
		var b strings.Builder
		b.WriteString("{")
		for i := range 200 {
			if i > 0 {
				b.WriteString(",")
			}
			name := i
			if dup && i == 199 {
				name = 3
			}
			fmt.Fprintf(&b, `"name%d":{"inner%d":[1,{"x":%d}]}`, name, i, i)
		}
		b.WriteString("}")
		compareValidate(t, []byte(b.String()), JSONOptions{MaxBytes: 1 << 16, MaxDepth: 16, MaxMembers: 8192})
	}

	// Random mutations of the seeds, which is where a scanner's off-by-ones
	// live.
	rng := rand.New(rand.NewSource(1))
	alphabet := []byte("{}[]\":,\\ \n\t01-+.eEabftnrulsxu\x00\x1f\x7f\xc3\xa9\xff")
	for range 20000 {
		seed := []byte(jsonCases[rng.Intn(len(jsonCases))])
		doc := append([]byte(nil), seed...)
		for range 1 + rng.Intn(3) {
			switch rng.Intn(3) {
			case 0:
				if len(doc) > 0 {
					doc[rng.Intn(len(doc))] = alphabet[rng.Intn(len(alphabet))]
				}
			case 1:
				at := rng.Intn(len(doc) + 1)
				doc = append(doc[:at], append([]byte{alphabet[rng.Intn(len(alphabet))]}, doc[at:]...)...)
			case 2:
				if len(doc) > 0 {
					at := rng.Intn(len(doc))
					doc = append(doc[:at], doc[at+1:]...)
				}
			}
		}
		compareValidate(t, doc, JSONOptions{MaxBytes: 4096, MaxDepth: 1 + rng.Intn(6), MaxMembers: 1 + rng.Intn(8)})
	}
}

func compareValidate(t *testing.T, doc []byte, options JSONOptions) {
	t.Helper()
	got := verdict(ValidateJSON(doc, options))
	want := verdict(referenceValidateJSON(doc, options))
	if got != want {
		t.Fatalf("ValidateJSON(%q, %+v) = %s, reference %s", doc, options, got, want)
	}
}

func TestObjectMembersAndUnquoteMatchEncodingJSON(t *testing.T) {
	options := JSONOptions{MaxBytes: 4096, MaxDepth: 16, MaxMembers: 256}
	docs := []string{
		`{"alg":"HS256","typ":"JWT","kid":"k\u00e9y","crit":["b64"],"n":1,"o":{"a":[1,2]},"z":null}`,
		`{"a\u0062":"\ud83d\ude00","c":"\ud83d","d":"\udc00x","e":"` + "\xff" + `","f":"\u0000","g":"\/\\\"\b\f\n\r\t"}`,
		`{}`, ` { "only" : [ ] } `,
	}
	for _, doc := range docs {
		var want map[string]json.RawMessage
		if err := json.Unmarshal([]byte(doc), &want); err != nil {
			t.Fatal(err)
		}
		got := map[string]json.RawMessage{}
		var order []string
		err := ObjectMembers([]byte(doc), options, func(name, value []byte) error {
			got[string(name)] = append(json.RawMessage(nil), value...)
			order = append(order, string(name))
			return nil
		})
		if err != nil {
			t.Fatalf("ObjectMembers(%q): %v", doc, err)
		}
		if len(got) != len(want) {
			t.Fatalf("ObjectMembers(%q) saw %v, want %v", doc, order, want)
		}
		for name, value := range want {
			if !bytes.Equal(got[name], value) {
				t.Fatalf("ObjectMembers(%q) member %q = %s, want %s", doc, name, got[name], value)
			}
			if value[0] != '"' {
				continue
			}
			var wantString string
			if err := json.Unmarshal(value, &wantString); err != nil {
				t.Fatal(err)
			}
			if gotString, ok := Unquote(value); !ok || gotString != wantString {
				t.Fatalf("Unquote(%s) = %q, %v, want %q", value, gotString, ok, wantString)
			}
		}
	}
	// A name that is not valid UTF-8 is handed over decoded, which the
	// fuzzer found being sliced from the document at the decoded length.
	invalid := "{\"t\xd3\xce\":1}"
	err := ObjectMembers([]byte(invalid), options, func(name, value []byte) error {
		if string(name) != "t\ufffd\ufffd" || string(value) != "1" {
			t.Fatalf("invalid UTF-8 name = %q, value %s", name, value)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, doc := range []string{`[]`, `1`, `"s"`, `{"a":1,"a":2}`, `{"a":1}x`, `{"a":1`} {
		if err := ObjectMembers([]byte(doc), options, func(name, value []byte) error { return nil }); err == nil {
			t.Fatalf("ObjectMembers(%q) accepted", doc)
		}
	}
	stop := errors.New("stop")
	if err := ObjectMembers([]byte(`{"a":1,"b":2}`), options, func(name, value []byte) error { return stop }); !errors.Is(err, stop) {
		t.Fatalf("visitor error = %v", err)
	}
}

func TestArrayElements(t *testing.T) {
	var got []string
	err := ArrayElements([]byte(` [ "a" , [1,2] , {"k":"v"} , null , -1.5e3 ] `), func(elem []byte) error {
		got = append(got, string(elem))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{`"a"`, `[1,2]`, `{"k":"v"}`, `null`, `-1.5e3`}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("elements = %q, want %q", got, want)
	}
	if err := ArrayElements([]byte(`[]`), func([]byte) error { t.Fatal("visited"); return nil }); err != nil {
		t.Fatal(err)
	}
	if err := ArrayElements([]byte(`{"a":1}`), func([]byte) error { return nil }); err == nil {
		t.Fatal("object accepted as array")
	}
}

func TestValidateJSONAllocatesNothingForATokenObject(t *testing.T) {
	doc := []byte(`{"iss":"https://issuer.example","sub":"device-42","aud":["https://api.example"],"exp":4102444800,"nbf":1,"iat":1,"jti":"id","scope":"a b c","nested":{"x":[1,2,3]}}`)
	options := JSONOptions{MaxBytes: 4096, MaxDepth: 16, MaxMembers: 256}
	if allocs := testing.AllocsPerRun(100, func() {
		if err := ValidateJSON(doc, options); err != nil {
			t.Fatal(err)
		}
	}); allocs != 0 {
		t.Fatalf("ValidateJSON allocated %.0f times", allocs)
	}
}
