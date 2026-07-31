package dynamodb

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
)

// Errors from the attribute codec itself, as opposed to a DynamoDB reply.
var (
	// ErrEmptyAttribute is an AttributeValue with no field set. DynamoDB has no
	// encoding for "no type", so this is a programming error rather than a NULL.
	ErrEmptyAttribute = errors.New("dynamodb: attribute value has no type set")

	// ErrAmbiguousAttribute is an AttributeValue with more than one field set.
	// The wire form carries exactly one member, so there is no correct choice
	// to make here.
	ErrAmbiguousAttribute = errors.New("dynamodb: attribute value has more than one type set")
)

// Kind is which of the DynamoDB types an AttributeValue carries.
type Kind int

// The DynamoDB attribute types. KindNone is the zero AttributeValue, which is
// not a valid wire value.
const (
	KindNone Kind = iota
	KindString
	KindNumber
	KindBinary
	KindBool
	KindNull
	KindList
	KindMap
	KindStringSet
	KindNumberSet
	KindBinarySet
)

// AttributeValue is one attribute in its wire form. Exactly one field is set;
// which one is the attribute's type.
//
// The codec is written out rather than derived by reflection, so the supported
// type set is visible in this declaration and an unsupported type is a compile
// error rather than a runtime surprise. MarshalItem converts Go structs for
// callers who want that instead.
//
// Numbers are held as text. DynamoDB numbers carry up to 38 significant digits,
// which float64 cannot represent, so conversion happens in AsInt and AsFloat
// where the caller has chosen the Go type that loses the precision.
type AttributeValue struct {
	S    *string                   // string
	N    *string                   // number, as text
	B    []byte                    // binary
	BOOL *bool                     // boolean
	NULL bool                      // the null attribute
	L    []AttributeValue          // list
	M    map[string]AttributeValue // map
	SS   []string                  // string set
	NS   []string                  // number set
	BS   [][]byte                  // binary set
}

// Item is a DynamoDB item: attribute names to values.
type Item = map[string]AttributeValue

// Key is the primary key of one item: the partition attribute, and the sort
// attribute when the table has one.
type Key = map[string]AttributeValue

// Number is what N accepts.
type Number interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 |
		~float32 | ~float64
}

// S returns a string attribute. The empty string is a valid value and is stored
// as one.
func S(v string) AttributeValue { return AttributeValue{S: &v} }

// N returns a number attribute, formatted without loss for the Go type given.
func N[T Number](v T) AttributeValue {
	var text string
	switch n := any(v).(type) {
	case float32:
		text = strconv.FormatFloat(float64(n), 'g', -1, 32)
	case float64:
		text = strconv.FormatFloat(n, 'g', -1, 64)
	case uint, uint8, uint16, uint32, uint64:
		text = fmt.Sprintf("%d", n)
	default:
		text = fmt.Sprintf("%d", n)
	}
	return AttributeValue{N: &text}
}

// NString returns a number attribute from text already in DynamoDB's format,
// which is how a value with more precision than float64 gets in.
func NString(v string) AttributeValue { return AttributeValue{N: &v} }

// B returns a binary attribute.
func B(v []byte) AttributeValue {
	if v == nil {
		v = []byte{}
	}
	return AttributeValue{B: v}
}

// Bool returns a boolean attribute.
func Bool(v bool) AttributeValue { return AttributeValue{BOOL: &v} }

// Null returns the null attribute.
func Null() AttributeValue { return AttributeValue{NULL: true} }

// List returns a list attribute.
func List(vs ...AttributeValue) AttributeValue {
	if vs == nil {
		vs = []AttributeValue{}
	}
	return AttributeValue{L: vs}
}

// Map returns a map attribute.
func Map(m map[string]AttributeValue) AttributeValue {
	if m == nil {
		m = map[string]AttributeValue{}
	}
	return AttributeValue{M: m}
}

// SS returns a string set. DynamoDB rejects an empty set.
func SS(vs ...string) AttributeValue { return AttributeValue{SS: vs} }

// NS returns a number set from values already in DynamoDB's number format.
func NS(vs ...string) AttributeValue { return AttributeValue{NS: vs} }

// BS returns a binary set.
func BS(vs ...[]byte) AttributeValue { return AttributeValue{BS: vs} }

// Kind reports which type a carries.
func (a AttributeValue) Kind() Kind {
	switch {
	case a.S != nil:
		return KindString
	case a.N != nil:
		return KindNumber
	case a.B != nil:
		return KindBinary
	case a.BOOL != nil:
		return KindBool
	case a.NULL:
		return KindNull
	case a.L != nil:
		return KindList
	case a.M != nil:
		return KindMap
	case a.SS != nil:
		return KindStringSet
	case a.NS != nil:
		return KindNumberSet
	case a.BS != nil:
		return KindBinarySet
	}
	return KindNone
}

// AsString returns the string value, and whether the attribute held one.
func (a AttributeValue) AsString() (string, bool) {
	if a.S == nil {
		return "", false
	}
	return *a.S, true
}

// AsInt parses the number as an int64. It reports false for a non-number and
// for a number that does not fit, which includes any value with a fractional
// part.
func (a AttributeValue) AsInt() (int64, bool) {
	if a.N == nil {
		return 0, false
	}
	n, err := strconv.ParseInt(*a.N, 10, 64)
	return n, err == nil
}

// AsFloat parses the number as a float64, losing precision beyond 15 digits.
func (a AttributeValue) AsFloat() (float64, bool) {
	if a.N == nil {
		return 0, false
	}
	f, err := strconv.ParseFloat(*a.N, 64)
	return f, err == nil
}

// AsNumber returns the number as the text DynamoDB stored, which is the only
// lossless form.
func (a AttributeValue) AsNumber() (string, bool) {
	if a.N == nil {
		return "", false
	}
	return *a.N, true
}

// AsBytes returns the binary value.
func (a AttributeValue) AsBytes() ([]byte, bool) {
	if a.B == nil {
		return nil, false
	}
	return a.B, true
}

// AsBool returns the boolean value.
func (a AttributeValue) AsBool() (bool, bool) {
	if a.BOOL == nil {
		return false, false
	}
	return *a.BOOL, true
}

// AsList returns the list value.
func (a AttributeValue) AsList() ([]AttributeValue, bool) {
	if a.L == nil {
		return nil, false
	}
	return a.L, true
}

// AsMap returns the map value.
func (a AttributeValue) AsMap() (map[string]AttributeValue, bool) {
	if a.M == nil {
		return nil, false
	}
	return a.M, true
}

// IsNull reports whether the attribute is the null attribute, which is not the
// same as being absent from an item.
func (a AttributeValue) IsNull() bool { return a.NULL }

// set counts the fields carrying a value, so the codec can reject an ambiguous
// attribute instead of silently picking one.
func (a AttributeValue) set() int {
	n := 0
	for _, ok := range []bool{
		a.S != nil, a.N != nil, a.B != nil, a.BOOL != nil, a.NULL,
		a.L != nil, a.M != nil, a.SS != nil, a.NS != nil, a.BS != nil,
	} {
		if ok {
			n++
		}
	}
	return n
}

// MarshalJSON writes the single-member object DynamoDB expects.
func (a AttributeValue) MarshalJSON() ([]byte, error) {
	switch n := a.set(); {
	case n == 0:
		return nil, ErrEmptyAttribute
	case n > 1:
		return nil, ErrAmbiguousAttribute
	}

	switch a.Kind() {
	case KindString:
		return json.Marshal(map[string]string{"S": *a.S})
	case KindNumber:
		return json.Marshal(map[string]string{"N": *a.N})
	case KindBinary:
		return json.Marshal(map[string][]byte{"B": a.B})
	case KindBool:
		return json.Marshal(map[string]bool{"BOOL": *a.BOOL})
	case KindNull:
		return json.Marshal(map[string]bool{"NULL": true})
	case KindList:
		return json.Marshal(map[string][]AttributeValue{"L": a.L})
	case KindMap:
		return json.Marshal(map[string]map[string]AttributeValue{"M": a.M})
	case KindStringSet:
		return json.Marshal(map[string][]string{"SS": a.SS})
	case KindNumberSet:
		return json.Marshal(map[string][]string{"NS": a.NS})
	case KindBinarySet:
		return json.Marshal(map[string][][]byte{"BS": a.BS})
	}
	return nil, ErrEmptyAttribute
}

// UnmarshalJSON reads the single-member object DynamoDB sends. An unknown
// member is an error rather than an ignored field: a type this package does not
// know about would otherwise decode to an empty attribute.
func (a *AttributeValue) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if len(raw) != 1 {
		return fmt.Errorf("dynamodb: attribute value has %d members, want 1", len(raw))
	}

	*a = AttributeValue{}
	for name, value := range raw {
		switch name {
		case "S":
			var v string
			if err := json.Unmarshal(value, &v); err != nil {
				return err
			}
			a.S = &v
		case "N":
			var v string
			if err := json.Unmarshal(value, &v); err != nil {
				return err
			}
			a.N = &v
		case "B":
			return json.Unmarshal(value, &a.B)
		case "BOOL":
			var v bool
			if err := json.Unmarshal(value, &v); err != nil {
				return err
			}
			a.BOOL = &v
		case "NULL":
			var v bool
			if err := json.Unmarshal(value, &v); err != nil {
				return err
			}
			a.NULL = v
			if !v {
				// {"NULL": false} is not a value DynamoDB sends, and taking it
				// as an attribute would produce one with nothing set.
				return errors.New(`dynamodb: {"NULL":false} is not an attribute value`)
			}
		case "L":
			return json.Unmarshal(value, &a.L)
		case "M":
			return json.Unmarshal(value, &a.M)
		case "SS":
			return json.Unmarshal(value, &a.SS)
		case "NS":
			return json.Unmarshal(value, &a.NS)
		case "BS":
			return json.Unmarshal(value, &a.BS)
		default:
			return fmt.Errorf("dynamodb: unknown attribute type %q", name)
		}
	}
	return nil
}
