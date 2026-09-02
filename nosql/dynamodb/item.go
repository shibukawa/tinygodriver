package dynamodb

// Struct mapping, kept in its own file because it is the only place this
// package uses reflection.
//
// AttributeValue and its constructors carry no reflection at all, so a program
// that never calls MarshalItem or UnmarshalItem does not link the code below.
// That is the point of the split: reflection costs binary size, and this
// package exists for builds where that is a real constraint.

import (
	"fmt"
	"reflect"
	"strconv"
	"time"

	"github.com/shibukawa/tinygodriver/internal/syncx"
)

// itemTag is the struct tag, spelled as in aws-sdk-go-v2 so an example written
// against the SDK ports over unchanged.
const itemTag = "dynamodbav"

// The two types the codec special-cases, resolved once. reflect.TypeOf per
// value was measurable on items with many attributes.
var (
	timeType      = reflect.TypeOf(time.Time{})
	attributeType = reflect.TypeOf(AttributeValue{})
)

// MarshalItem converts a struct, or a map[string]AttributeValue, into an item.
//
// Fields are named by their dynamodbav tag, or by the field name when there is
// none. A tag of "-" skips the field, and ",omitempty" skips it when it holds
// the zero value.
//
// The supported types are string, the integer and float kinds, bool, []byte,
// time.Time (RFC 3339 text), slices, maps with string keys, structs, and
// pointers to any of those. A nil pointer becomes the null attribute.
func MarshalItem(v any) (Item, error) {
	if item, ok := v.(Item); ok {
		return item, nil
	}
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return nil, fmt.Errorf("dynamodb: MarshalItem(nil %T)", v)
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return nil, fmt.Errorf("dynamodb: MarshalItem wants a struct, got %s", rv.Kind())
	}
	value, err := marshalValue(rv)
	if err != nil {
		return nil, err
	}
	return value.M, nil
}

// UnmarshalItem fills a struct, or a *map[string]AttributeValue, from an item.
// Attributes with no matching field are ignored; fields with no matching
// attribute are left alone.
func UnmarshalItem(item Item, out any) error {
	if target, ok := out.(*Item); ok {
		*target = item
		return nil
	}
	rv := reflect.ValueOf(out)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return fmt.Errorf("dynamodb: UnmarshalItem wants a non-nil pointer, got %T", out)
	}
	return unmarshalValue(AttributeValue{M: item}, rv.Elem())
}

// fieldInfo is one marshalable struct field, with its tag already parsed.
type fieldInfo struct {
	index     int
	name      string
	omitEmpty bool
}

// fieldCache maps a struct type to its parsed fields. Tag parsing per field
// per call dominated repeated marshaling of the same type, which is the
// ordinary shape of a program that talks to one table. A plain map under an
// RWMutex rather than sync.Map: the read path is one RLock, and sync.Map is
// measurably more binary in a TinyGo build.
var fieldCache struct {
	syncx.RWMutex
	types map[reflect.Type][]fieldInfo
}

func cachedFields(t reflect.Type) []fieldInfo {
	fieldCache.RLock()
	fields, ok := fieldCache.types[t]
	fieldCache.RUnlock()
	if ok {
		return fields
	}
	fields = make([]fieldInfo, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		name, skip, omitEmpty := fieldName(t.Field(i))
		if skip {
			continue
		}
		fields = append(fields, fieldInfo{index: i, name: name, omitEmpty: omitEmpty})
	}
	fieldCache.Lock()
	if fieldCache.types == nil {
		fieldCache.types = map[reflect.Type][]fieldInfo{}
	}
	fieldCache.types[t] = fields
	fieldCache.Unlock()
	return fields
}

// fieldName returns the attribute name for a struct field, whether it is
// skipped, and whether it is omitted when empty.
func fieldName(f reflect.StructField) (name string, skip, omitEmpty bool) {
	if f.PkgPath != "" {
		return "", true, false // unexported
	}
	tag := f.Tag.Get(itemTag)
	if tag == "-" {
		return "", true, false
	}
	name = f.Name
	for i, part := range splitComma(tag) {
		switch {
		case i == 0 && part != "":
			name = part
		case part == "omitempty":
			omitEmpty = true
		}
	}
	return name, false, omitEmpty
}

func splitComma(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

func marshalValue(rv reflect.Value) (AttributeValue, error) {
	if !rv.IsValid() {
		return Null(), nil
	}
	// time.Time is a struct, so it has to be recognized before the struct case.
	if rv.Type() == timeType {
		return S(rv.Interface().(time.Time).UTC().Format(time.RFC3339Nano)), nil
	}
	if rv.Type() == attributeType {
		return rv.Interface().(AttributeValue), nil
	}

	switch rv.Kind() {
	case reflect.Pointer, reflect.Interface:
		if rv.IsNil() {
			return Null(), nil
		}
		return marshalValue(rv.Elem())

	case reflect.String:
		return S(rv.String()), nil

	case reflect.Bool:
		return Bool(rv.Bool()), nil

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return NString(strconv.FormatInt(rv.Int(), 10)), nil

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return NString(strconv.FormatUint(rv.Uint(), 10)), nil

	case reflect.Float32:
		return NString(strconv.FormatFloat(rv.Float(), 'g', -1, 32)), nil

	case reflect.Float64:
		return NString(strconv.FormatFloat(rv.Float(), 'g', -1, 64)), nil

	case reflect.Slice, reflect.Array:
		if rv.Type().Elem().Kind() == reflect.Uint8 && rv.Kind() == reflect.Slice {
			return B(rv.Bytes()), nil
		}
		list := make([]AttributeValue, 0, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			element, err := marshalValue(rv.Index(i))
			if err != nil {
				return AttributeValue{}, err
			}
			list = append(list, element)
		}
		return List(list...), nil

	case reflect.Map:
		if rv.Type().Key().Kind() != reflect.String {
			return AttributeValue{}, fmt.Errorf("dynamodb: map key must be a string, got %s", rv.Type().Key())
		}
		m := make(map[string]AttributeValue, rv.Len())
		iter := rv.MapRange()
		for iter.Next() {
			element, err := marshalValue(iter.Value())
			if err != nil {
				return AttributeValue{}, err
			}
			m[iter.Key().String()] = element
		}
		return Map(m), nil

	case reflect.Struct:
		fields := cachedFields(rv.Type())
		m := make(map[string]AttributeValue, len(fields))
		for _, f := range fields {
			fv := rv.Field(f.index)
			if f.omitEmpty && fv.IsZero() {
				continue
			}
			element, err := marshalValue(fv)
			if err != nil {
				return AttributeValue{}, err
			}
			m[f.name] = element
		}
		return Map(m), nil
	}
	return AttributeValue{}, fmt.Errorf("dynamodb: cannot marshal %s", rv.Kind())
}

func unmarshalValue(av AttributeValue, rv reflect.Value) error {
	if !rv.CanSet() {
		return fmt.Errorf("dynamodb: cannot set %s", rv.Type())
	}
	if rv.Type() == attributeType {
		rv.Set(reflect.ValueOf(av))
		return nil
	}
	if av.IsNull() {
		rv.Set(reflect.Zero(rv.Type()))
		return nil
	}
	if rv.Type() == timeType {
		text, ok := av.AsString()
		if !ok {
			return typeError(av, rv)
		}
		when, err := time.Parse(time.RFC3339Nano, text)
		if err != nil {
			return err
		}
		rv.Set(reflect.ValueOf(when))
		return nil
	}

	switch rv.Kind() {
	case reflect.Pointer:
		if rv.IsNil() {
			rv.Set(reflect.New(rv.Type().Elem()))
		}
		return unmarshalValue(av, rv.Elem())

	case reflect.Interface:
		if rv.NumMethod() != 0 {
			return typeError(av, rv)
		}
		rv.Set(reflect.ValueOf(av))
		return nil

	case reflect.String:
		text, ok := av.AsString()
		if !ok {
			return typeError(av, rv)
		}
		rv.SetString(text)
		return nil

	case reflect.Bool:
		b, ok := av.AsBool()
		if !ok {
			return typeError(av, rv)
		}
		rv.SetBool(b)
		return nil

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, ok := av.AsInt()
		if !ok {
			return typeError(av, rv)
		}
		if rv.OverflowInt(n) {
			return fmt.Errorf("dynamodb: %d overflows %s", n, rv.Type())
		}
		rv.SetInt(n)
		return nil

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		text, ok := av.AsNumber()
		if !ok {
			return typeError(av, rv)
		}
		n, err := strconv.ParseUint(text, 10, 64)
		if err != nil {
			return err
		}
		if rv.OverflowUint(n) {
			return fmt.Errorf("dynamodb: %d overflows %s", n, rv.Type())
		}
		rv.SetUint(n)
		return nil

	case reflect.Float32, reflect.Float64:
		f, ok := av.AsFloat()
		if !ok {
			return typeError(av, rv)
		}
		if rv.OverflowFloat(f) {
			return fmt.Errorf("dynamodb: %v overflows %s", f, rv.Type())
		}
		rv.SetFloat(f)
		return nil

	case reflect.Slice:
		if rv.Type().Elem().Kind() == reflect.Uint8 {
			b, ok := av.AsBytes()
			if !ok {
				return typeError(av, rv)
			}
			rv.SetBytes(b)
			return nil
		}
		list, ok := av.AsList()
		if !ok {
			if set, isSet := av.SS, av.SS != nil; isSet && rv.Type().Elem().Kind() == reflect.String {
				rv.Set(reflect.ValueOf(set))
				return nil
			}
			return typeError(av, rv)
		}
		out := reflect.MakeSlice(rv.Type(), len(list), len(list))
		for i, element := range list {
			if err := unmarshalValue(element, out.Index(i)); err != nil {
				return err
			}
		}
		rv.Set(out)
		return nil

	case reflect.Map:
		m, ok := av.AsMap()
		if !ok {
			return typeError(av, rv)
		}
		if rv.Type().Key().Kind() != reflect.String {
			return fmt.Errorf("dynamodb: map key must be a string, got %s", rv.Type().Key())
		}
		out := reflect.MakeMapWithSize(rv.Type(), len(m))
		for name, element := range m {
			value := reflect.New(rv.Type().Elem()).Elem()
			if err := unmarshalValue(element, value); err != nil {
				return err
			}
			out.SetMapIndex(reflect.ValueOf(name).Convert(rv.Type().Key()), value)
		}
		rv.Set(out)
		return nil

	case reflect.Struct:
		m, ok := av.AsMap()
		if !ok {
			return typeError(av, rv)
		}
		for _, f := range cachedFields(rv.Type()) {
			element, present := m[f.name]
			if !present {
				continue
			}
			if err := unmarshalValue(element, rv.Field(f.index)); err != nil {
				return fmt.Errorf("%s: %w", f.name, err)
			}
		}
		return nil
	}
	return fmt.Errorf("dynamodb: cannot unmarshal into %s", rv.Kind())
}

func typeError(av AttributeValue, rv reflect.Value) error {
	return fmt.Errorf("dynamodb: cannot unmarshal %s into %s", kindName(av.Kind()), rv.Type())
}

func kindName(k Kind) string {
	switch k {
	case KindString:
		return "S"
	case KindNumber:
		return "N"
	case KindBinary:
		return "B"
	case KindBool:
		return "BOOL"
	case KindNull:
		return "NULL"
	case KindList:
		return "L"
	case KindMap:
		return "M"
	case KindStringSet:
		return "SS"
	case KindNumberSet:
		return "NS"
	case KindBinarySet:
		return "BS"
	}
	return "an empty attribute"
}
