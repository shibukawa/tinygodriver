package datastore

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// Codec failures. A Value carries exactly one member on the wire, because it is
// a proto3 oneof, so neither zero nor two is something this package can encode
// on the caller's behalf.
var (
	ErrEmptyValue     = errors.New("datastore: value has no member set")
	ErrAmbiguousValue = errors.New("datastore: value has more than one member set")
	ErrBadValue       = errors.New("datastore: malformed value on the wire")
)

// Kind identifies which member of a Value is set.
type Kind int

// The Value kinds, in the order the wire format lists them.
const (
	KindInvalid Kind = iota
	KindNull
	KindBool
	KindInteger
	KindDouble
	KindTimestamp
	KindKey
	KindString
	KindBlob
	KindGeoPoint
	KindEntity
	KindArray
)

// String names the kind, for error messages.
func (k Kind) String() string {
	switch k {
	case KindNull:
		return "null"
	case KindBool:
		return "boolean"
	case KindInteger:
		return "integer"
	case KindDouble:
		return "double"
	case KindTimestamp:
		return "timestamp"
	case KindKey:
		return "key"
	case KindString:
		return "string"
	case KindBlob:
		return "blob"
	case KindGeoPoint:
		return "geoPoint"
	case KindEntity:
		return "entity"
	case KindArray:
		return "array"
	}
	return "invalid"
}

// LatLng is a geographical point.
type LatLng struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

// Integer is the set of Go integer types Int accepts.
//
// Every member fits int64 on every platform, which is what makes Int total: it
// cannot be handed a value Datastore has no representation for.
//
// ~uint was removed on 2026-08-04. On a 64-bit platform uint holds values int64
// does not, and Int converted through int64, so Int(uint(math.MaxUint64))
// stored "-1" and reported no error — the one silent wrong write in a package
// that refuses out-of-range integer text, refuses widening a double to an
// integer, and refuses a uint64 in the struct mapper. Int returns no error, so
// the constraint is the only place this could be fixed without changing the
// signature.
//
// A uint caller writes Int(int64(n)) when the value is known to fit, or
// IntString(strconv.FormatUint(n, 10)) when it is not — and the latter fails
// loudly at encode time if it really is too wide. 32-bit callers, where uint
// always fits, pay a conversion for that.
type Integer interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 | ~uint8 | ~uint16 | ~uint32
}

// Value is one property in its wire form. Exactly one member is set; a slice
// member counts as set when it is non-nil, so an empty array and an absent one
// are different values.
//
// Integer is text because proto3 JSON encodes int64 as a string, which is also
// what keeps a 64-bit id from passing through float64 on the way in. Double is
// a real JSON number: Datastore stores the two as different types, and
// collapsing them would change sort order and equality filters.
type Value struct {
	Null      bool
	Bool      *bool
	Integer   *string
	Double    *float64
	Timestamp *time.Time
	Key       *Key
	String    *string
	Blob      []byte
	GeoPoint  *LatLng
	Entity    *Entity
	Array     []Value

	// ExcludeFromIndexes is not part of the union. It rides alongside whichever
	// member is set.
	ExcludeFromIndexes bool
}

// String builds a string value.
func String(v string) Value { return Value{String: &v} }

// Int builds an integer value from any Go integer type.
func Int[T Integer](v T) Value {
	s := strconv.FormatInt(int64(v), 10)
	return Value{Integer: &s}
}

// IntString builds an integer value from its decimal text, for a uint64 beyond
// int64 or to avoid a conversion the caller does not want.
func IntString(v string) Value { return Value{Integer: &v} }

// Float builds a double value.
func Float(v float64) Value { return Value{Double: &v} }

// Bool builds a boolean value.
func Bool(v bool) Value { return Value{Bool: &v} }

// Time builds a timestamp value.
//
// Datastore stores microseconds, so a value with finer resolution loses it on
// the round trip. That truncation happens on the server and is not hidden here.
func Time(v time.Time) Value { return Value{Timestamp: &v} }

// Blob builds a byte-string value, base64 on the wire.
func Blob(v []byte) Value {
	if v == nil {
		v = []byte{}
	}
	return Value{Blob: v}
}

// KeyValue builds a value referring to another entity.
func KeyValue(k Key) Value { return Value{Key: &k} }

// GeoPoint builds a geographical point value.
func GeoPoint(lat, lng float64) Value {
	return Value{GeoPoint: &LatLng{Latitude: lat, Longitude: lng}}
}

// Nested builds a value holding an embedded entity.
//
// An embedded entity has no key. One that carries a key is rejected at encode
// time rather than silently stripped.
func Nested(e Entity) Value { return Value{Entity: &e} }

// Array builds a list value. An array of zero values is legal and is distinct
// from an absent property.
func Array(vs ...Value) Value {
	if vs == nil {
		vs = []Value{}
	}
	return Value{Array: vs}
}

// Null builds the null value, which is distinct from an absent property.
func Null() Value { return Value{Null: true} }

// Unindexed returns v with ExcludeFromIndexes set. It composes with every
// constructor, because indexing is not one of the union members.
func Unindexed(v Value) Value {
	v.ExcludeFromIndexes = true
	return v
}

// Kind reports which member is set, or KindInvalid when zero or several are.
func (v Value) Kind() Kind {
	kind, n := v.kind()
	if n != 1 {
		return KindInvalid
	}
	return kind
}

func (v Value) kind() (Kind, int) {
	kind, n := KindInvalid, 0
	if v.Null {
		kind, n = KindNull, n+1
	}
	if v.Bool != nil {
		kind, n = KindBool, n+1
	}
	if v.Integer != nil {
		kind, n = KindInteger, n+1
	}
	if v.Double != nil {
		kind, n = KindDouble, n+1
	}
	if v.Timestamp != nil {
		kind, n = KindTimestamp, n+1
	}
	if v.Key != nil {
		kind, n = KindKey, n+1
	}
	if v.String != nil {
		kind, n = KindString, n+1
	}
	if v.Blob != nil {
		kind, n = KindBlob, n+1
	}
	if v.GeoPoint != nil {
		kind, n = KindGeoPoint, n+1
	}
	if v.Entity != nil {
		kind, n = KindEntity, n+1
	}
	if v.Array != nil {
		kind, n = KindArray, n+1
	}
	return kind, n
}

// MarshalJSON emits exactly one union member, plus excludeFromIndexes when set.
func (v Value) MarshalJSON() ([]byte, error) {
	kind, n := v.kind()
	switch {
	case n == 0:
		return nil, ErrEmptyValue
	case n > 1:
		return nil, ErrAmbiguousValue
	}

	out := make([]byte, 0, 48)
	out = append(out, '{')
	out, err := v.appendMember(out, kind)
	if err != nil {
		return nil, err
	}
	if v.ExcludeFromIndexes {
		out = append(out, `,"excludeFromIndexes":true`...)
	}
	out = append(out, '}')
	return out, nil
}

// appendMember renders the union member into dst. Appending into the caller's
// buffer keeps each encoded property to one allocation beyond json.Marshal's
// own, where the member used to pass through two intermediates on the way out.
func (v Value) appendMember(dst []byte, kind Kind) ([]byte, error) {
	encode := func(name string, value any) ([]byte, error) {
		body, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		dst = append(dst, '"')
		dst = append(dst, name...)
		dst = append(dst, `":`...)
		return append(dst, body...), nil
	}
	switch kind {
	case KindNull:
		return append(dst, `"nullValue":null`...), nil
	case KindBool:
		return encode("booleanValue", *v.Bool)
	case KindInteger:
		// Text on the wire, and validated here rather than at the server: an
		// unparseable integer is a caller mistake worth catching locally.
		if _, err := strconv.ParseInt(*v.Integer, 10, 64); err != nil {
			return nil, fmt.Errorf("%w: integerValue %q is not an int64", ErrBadValue, *v.Integer)
		}
		return encode("integerValue", *v.Integer)
	case KindDouble:
		return encode("doubleValue", *v.Double)
	case KindTimestamp:
		return encode("timestampValue", v.Timestamp.UTC().Format(time.RFC3339Nano))
	case KindKey:
		return encode("keyValue", v.Key)
	case KindString:
		return encode("stringValue", *v.String)
	case KindBlob:
		return encode("blobValue", base64.StdEncoding.EncodeToString(v.Blob))
	case KindGeoPoint:
		return encode("geoPointValue", v.GeoPoint)
	case KindEntity:
		if v.Entity.Key != nil {
			return nil, fmt.Errorf("%w: an embedded entity must not carry a key", ErrBadValue)
		}
		return encode("entityValue", v.Entity)
	case KindArray:
		body, err := json.Marshal(struct {
			Values []Value `json:"values"`
		}{Values: v.Array})
		if err != nil {
			return nil, err
		}
		dst = append(dst, `"arrayValue":`...)
		return append(dst, body...), nil
	}
	return nil, ErrEmptyValue
}

type wireArray struct {
	Values []Value `json:"values"`
}

// nonUnionMembers ride alongside the union member rather than being one.
// meaning is a deprecated field the server still sends on some values; it is
// read and discarded so its presence does not look like a second member.
var nonUnionMembers = map[string]bool{
	"excludeFromIndexes": true,
	"meaning":            true,
}

// valueMember is one member located by the scanning fast path.
type valueMember struct {
	name  []byte
	value []byte
}

// scanValueMembers locates the members of a small JSON object without
// decoding it into a map. It reports ok only for the shape it is certain of:
// plain (escape-free) member names, well-formed values, no duplicates, and no
// more members than a Value object can legitimately carry. Anything else goes
// back through the map path, which reports it exactly as before.
func scanValueMembers(b []byte, members []valueMember) ([]valueMember, bool) {
	i := skipJSONSpace(b, 0)
	if i >= len(b) || b[i] != '{' {
		return nil, false
	}
	i = skipJSONSpace(b, i+1)
	if i < len(b) && b[i] == '}' {
		return members, skipJSONSpace(b, i+1) == len(b)
	}
	for {
		if i >= len(b) || b[i] != '"' {
			return nil, false
		}
		j := i + 1
		for j < len(b) && b[j] != '"' {
			if b[j] == '\\' {
				return nil, false
			}
			j++
		}
		if j >= len(b) {
			return nil, false
		}
		name := b[i+1 : j]
		for _, m := range members {
			if string(m.name) == string(name) {
				// A duplicate collapses under map semantics; let the map do it.
				return nil, false
			}
		}
		if len(members) == cap(members) {
			return nil, false
		}
		i = skipJSONSpace(b, j+1)
		if i >= len(b) || b[i] != ':' {
			return nil, false
		}
		i = skipJSONSpace(b, i+1)
		end := scanJSONValue(b, i)
		if end < 0 {
			return nil, false
		}
		members = append(members, valueMember{name: name, value: b[i:end]})
		i = skipJSONSpace(b, end)
		if i >= len(b) {
			return nil, false
		}
		switch b[i] {
		case ',':
			i = skipJSONSpace(b, i+1)
		case '}':
			return members, skipJSONSpace(b, i+1) == len(b)
		default:
			return nil, false
		}
	}
}

// UnmarshalJSON reads exactly one union member, plus the non-union members
// that ride alongside it.
//
// The members are located by a direct scan when the object is plainly
// well-formed, and by decoding to a member map otherwise. The map alone was
// the allocation hot spot of reading any entity; the map path remains both
// the arbiter of anything the scanner is unsure about and the reason a struct
// of pointers cannot replace either: a struct cannot see nullValue at all,
// since encoding/json resolves a JSON null by setting the pointer field to
// nil, so the one member whose value is literally null becomes
// indistinguishable from an absent one. Counting keys also makes an unknown
// member an error instead of silently nothing.
func (v *Value) UnmarshalJSON(b []byte) error {
	var scratch [3]valueMember
	scanned, ok := scanValueMembers(b, scratch[:0])

	var efiRaw, nameBytes, raw []byte
	count := 0
	if ok {
		for _, m := range scanned {
			switch {
			case string(m.name) == "excludeFromIndexes":
				efiRaw = m.value
			case string(m.name) == "meaning":
			default:
				nameBytes, raw = m.name, m.value
				count++
			}
		}
	} else {
		var members map[string]json.RawMessage
		if err := json.Unmarshal(b, &members); err != nil {
			return fmt.Errorf("%w: %s", ErrBadValue, err)
		}
		efiRaw = members["excludeFromIndexes"]
		for key := range members {
			if nonUnionMembers[key] {
				continue
			}
			nameBytes, raw = []byte(key), members[key]
			count++
		}
	}

	out := Value{}
	if efiRaw != nil {
		if err := json.Unmarshal(efiRaw, &out.ExcludeFromIndexes); err != nil {
			return fmt.Errorf("%w: excludeFromIndexes", ErrBadValue)
		}
	}
	switch {
	case count == 0:
		return ErrEmptyValue
	case count > 1:
		return ErrAmbiguousValue
	}

	switch string(nameBytes) {
	case "nullValue":
		// The proto NullValue enum has exactly one JSON form. Anything else
		// under this name is not a value Datastore sends.
		if string(raw) != "null" {
			return fmt.Errorf("%w: nullValue must be null, got %s", ErrBadValue, raw)
		}
		out.Null = true
	case "booleanValue":
		var value bool
		if err := json.Unmarshal(raw, &value); err != nil {
			return fmt.Errorf("%w: booleanValue", ErrBadValue)
		}
		out.Bool = &value
	case "integerValue":
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return fmt.Errorf("%w: integerValue must be a string", ErrBadValue)
		}
		out.Integer = &value
	case "doubleValue":
		var value float64
		if err := json.Unmarshal(raw, &value); err != nil {
			return fmt.Errorf("%w: doubleValue", ErrBadValue)
		}
		out.Double = &value
	case "timestampValue":
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return fmt.Errorf("%w: timestampValue", ErrBadValue)
		}
		parsed, err := time.Parse(time.RFC3339Nano, text)
		if err != nil {
			return fmt.Errorf("%w: timestampValue %q", ErrBadValue, text)
		}
		out.Timestamp = &parsed
	case "keyValue":
		var key Key
		if err := json.Unmarshal(raw, &key); err != nil {
			return fmt.Errorf("%w: keyValue: %s", ErrBadValue, err)
		}
		out.Key = &key
	case "stringValue":
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return fmt.Errorf("%w: stringValue", ErrBadValue)
		}
		out.String = &value
	case "blobValue":
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return fmt.Errorf("%w: blobValue", ErrBadValue)
		}
		decoded, err := base64.StdEncoding.DecodeString(text)
		if err != nil {
			return fmt.Errorf("%w: blobValue is not base64", ErrBadValue)
		}
		if decoded == nil {
			decoded = []byte{}
		}
		out.Blob = decoded
	case "geoPointValue":
		var point LatLng
		if err := json.Unmarshal(raw, &point); err != nil {
			return fmt.Errorf("%w: geoPointValue", ErrBadValue)
		}
		out.GeoPoint = &point
	case "entityValue":
		var entity Entity
		if err := json.Unmarshal(raw, &entity); err != nil {
			return fmt.Errorf("%w: entityValue: %s", ErrBadValue, err)
		}
		out.Entity = &entity
	case "arrayValue":
		var array wireArray
		if err := json.Unmarshal(raw, &array); err != nil {
			return fmt.Errorf("%w: arrayValue: %s", ErrBadValue, err)
		}
		if array.Values == nil {
			array.Values = []Value{}
		}
		out.Array = array.Values
	default:
		return fmt.Errorf("%w: unknown member %q", ErrBadValue, nameBytes)
	}

	*v = out
	return nil
}

// AsString returns the string value.
func (v Value) AsString() (string, bool) {
	if v.String == nil {
		return "", false
	}
	return *v.String, true
}

// AsInt parses the integer value.
func (v Value) AsInt() (int64, bool) {
	if v.Integer == nil {
		return 0, false
	}
	n, err := strconv.ParseInt(*v.Integer, 10, 64)
	return n, err == nil
}

// AsNumber returns the integer value as stored text, without a conversion.
func (v Value) AsNumber() (string, bool) {
	if v.Integer == nil {
		return "", false
	}
	return *v.Integer, true
}

// AsFloat returns the double value.
//
// It does not accept an integer value. The two are distinct types to Datastore,
// and quietly widening one to the other is how a filter stops matching.
func (v Value) AsFloat() (float64, bool) {
	if v.Double == nil {
		return 0, false
	}
	return *v.Double, true
}

// AsBool returns the boolean value.
func (v Value) AsBool() (bool, bool) {
	if v.Bool == nil {
		return false, false
	}
	return *v.Bool, true
}

// AsTime returns the timestamp value.
func (v Value) AsTime() (time.Time, bool) {
	if v.Timestamp == nil {
		return time.Time{}, false
	}
	return *v.Timestamp, true
}

// AsBytes returns the blob value.
func (v Value) AsBytes() ([]byte, bool) {
	if v.Blob == nil {
		return nil, false
	}
	return v.Blob, true
}

// AsKey returns the key value.
func (v Value) AsKey() (Key, bool) {
	if v.Key == nil {
		return Key{}, false
	}
	return *v.Key, true
}

// AsGeoPoint returns the geographical point value.
func (v Value) AsGeoPoint() (LatLng, bool) {
	if v.GeoPoint == nil {
		return LatLng{}, false
	}
	return *v.GeoPoint, true
}

// AsEntity returns the embedded entity value.
func (v Value) AsEntity() (Entity, bool) {
	if v.Entity == nil {
		return Entity{}, false
	}
	return *v.Entity, true
}

// AsArray returns the list value.
func (v Value) AsArray() ([]Value, bool) {
	if v.Array == nil {
		return nil, false
	}
	return v.Array, true
}

// IsNull reports whether this is the null value, which is not the same as an
// absent property.
func (v Value) IsNull() bool { return v.Null }

// The three scanners below mirror the ones in nosql/dynamodb: enough JSON
// awareness to find a member's extent, no more. The value itself is judged by
// encoding/json when it is decoded, and a mis-scan surfaces there or in the
// framing checks of scanValueMembers, never silently.

func skipJSONSpace(data []byte, i int) int {
	for i < len(data) {
		switch data[i] {
		case ' ', '\t', '\r', '\n':
			i++
		default:
			return i
		}
	}
	return i
}

// scanJSONString returns the index just past the closing quote of the string
// starting at i, or -1 when it never closes.
func scanJSONString(data []byte, i int) int {
	i++ // opening quote
	for i < len(data) {
		switch data[i] {
		case '\\':
			i += 2
		case '"':
			return i + 1
		default:
			i++
		}
	}
	return -1
}

// scanJSONValue returns the index just past the JSON value starting at i, or
// -1 when no well-formed extent is found.
func scanJSONValue(data []byte, i int) int {
	if i >= len(data) {
		return -1
	}
	switch data[i] {
	case '"':
		return scanJSONString(data, i)
	case '{', '[':
		depth := 0
		for i < len(data) {
			switch data[i] {
			case '"':
				i = scanJSONString(data, i)
				if i < 0 {
					return -1
				}
			case '{', '[':
				depth++
				i++
			case '}', ']':
				depth--
				i++
				if depth == 0 {
					return i
				}
			default:
				i++
			}
		}
		return -1
	default:
		// A number, true, false, or null: it extends to the next structural
		// byte. An empty extent is not a value.
		start := i
		for i < len(data) {
			switch data[i] {
			case ',', '}', ']', ' ', '\t', '\r', '\n':
				if i == start {
					return -1
				}
				return i
			}
			i++
		}
		return i
	}
}
