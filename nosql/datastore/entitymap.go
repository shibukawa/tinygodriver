package datastore

// Struct mapping, kept in its own file because it is the only place this
// package uses reflection.
//
// Value and its constructors carry no reflection at all, so a program that
// never calls MarshalEntity or UnmarshalEntity does not link the code below.
// That is the point of the split: reflection costs binary size, and this
// package exists for builds where that is a real constraint.

import (
	"fmt"
	"reflect"
	"strconv"
	"sync"
	"time"
)

// entityTag is the struct tag, spelled as in cloud.google.com/go/datastore so
// an example written against the official client ports over unchanged.
//
// It is authoritative for this path only. A code generator over this driver
// reads its own tag, and a struct carrying both gets two field mappings that
// look interchangeable and disagree on every renamed property. If you generate
// a codec, treat a field carrying this tag but not yours as an error rather
// than as agreement.
const entityTag = "datastore"

// keyTagName marks the field holding the entity's own key. Datastore reserves
// __key__ as the pseudo-property naming the key in queries, so no real
// property can collide with it.
const keyTagName = "__key__"

// MarshalEntity converts a struct into an Entity.
//
// Fields are named by their datastore tag, or by the field name when there is
// none. A tag of "-" skips the field, ",omitempty" skips it when it holds the
// zero value, and ",noindex" sets ExcludeFromIndexes.
//
// A field tagged "__key__" carries the entity's key and must be a Key or *Key.
// Without one the returned Entity has no key, and the caller supplies it.
//
// The supported types are string, the integer and float kinds, bool, []byte,
// time.Time, Key, Value, slices, structs, and pointers to any of those. A nil
// pointer becomes the null value, which is distinct from an absent property;
// use ",omitempty" when absent is what you meant.
//
// Maps are deliberately unsupported. Datastore has no map type — a map would
// have to become an embedded entity, whose property names would then come from
// runtime data rather than from the struct, which is the one thing this
// mapping exists to avoid.
func MarshalEntity(v any) (Entity, error) {
	if e, ok := v.(Entity); ok {
		return e, nil
	}
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return Entity{}, fmt.Errorf("datastore: MarshalEntity(nil %T)", v)
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return Entity{}, fmt.Errorf("datastore: MarshalEntity wants a struct, got %s", rv.Kind())
	}
	return marshalStruct(rv)
}

// UnmarshalEntity fills a struct from an Entity.
//
// Properties with no matching field are ignored, and fields with no matching
// property are left alone: an entity of one kind need not carry the same
// properties as the next, so neither is an error.
//
// A field tagged "__key__" receives the entity's key.
func UnmarshalEntity(e Entity, out any) error {
	if target, ok := out.(*Entity); ok {
		*target = e
		return nil
	}
	rv := reflect.ValueOf(out)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return fmt.Errorf("datastore: UnmarshalEntity wants a non-nil pointer, got %T", out)
	}
	rv = rv.Elem()
	if rv.Kind() != reflect.Struct {
		return fmt.Errorf("datastore: UnmarshalEntity wants a pointer to a struct, got %s", rv.Kind())
	}
	return unmarshalStruct(e, rv)
}

// fieldSpec is what a struct tag says about one field.
type fieldSpec struct {
	name      string
	skip      bool
	omitEmpty bool
	noIndex   bool
	isKey     bool
}

// cachedField is one marshalable field of a struct type: its index and its
// parsed tag.
type cachedField struct {
	index int
	spec  fieldSpec
}

// fieldCache maps a struct type to its parsed fields, the same shape as the
// cache in nosql/dynamodb: tag parsing per field per call dominated repeated
// marshaling of the same type, which is the ordinary shape of a program that
// reads and writes one kind. A plain map under an RWMutex rather than
// sync.Map: the read path is one RLock, and sync.Map is measurably more
// binary in a TinyGo build.
var fieldCache struct {
	sync.RWMutex
	types map[reflect.Type][]cachedField
}

func cachedFields(t reflect.Type) []cachedField {
	fieldCache.RLock()
	fields, ok := fieldCache.types[t]
	fieldCache.RUnlock()
	if ok {
		return fields
	}
	fields = make([]cachedField, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		spec := specFor(t.Field(i))
		if spec.skip {
			continue
		}
		fields = append(fields, cachedField{index: i, spec: spec})
	}
	fieldCache.Lock()
	if fieldCache.types == nil {
		fieldCache.types = map[reflect.Type][]cachedField{}
	}
	fieldCache.types[t] = fields
	fieldCache.Unlock()
	return fields
}

func specFor(f reflect.StructField) fieldSpec {
	if f.PkgPath != "" {
		return fieldSpec{skip: true} // unexported
	}
	tag := f.Tag.Get(entityTag)
	if tag == "-" {
		return fieldSpec{skip: true}
	}
	spec := fieldSpec{name: f.Name}
	for i, part := range splitComma(tag) {
		switch {
		case i == 0 && part != "":
			spec.name = part
		case part == "omitempty":
			spec.omitEmpty = true
		case part == "noindex":
			spec.noIndex = true
		}
	}
	if spec.name == keyTagName {
		spec.isKey = true
	}
	return spec
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

var (
	timeType  = reflect.TypeOf(time.Time{})
	keyType   = reflect.TypeOf(Key{})
	valueType = reflect.TypeOf(Value{})
)

func marshalStruct(rv reflect.Value) (Entity, error) {
	out := Entity{Properties: map[string]Value{}}
	rt := rv.Type()
	for _, f := range cachedFields(rt) {
		spec := f.spec
		fv := rv.Field(f.index)

		if spec.isKey {
			key, err := keyFromField(fv)
			if err != nil {
				return Entity{}, err
			}
			out.Key = key
			continue
		}
		if spec.omitEmpty && fv.IsZero() {
			continue
		}
		value, err := marshalValue(fv)
		if err != nil {
			return Entity{}, fmt.Errorf("datastore: field %s: %w", rt.Field(f.index).Name, err)
		}
		if spec.noIndex {
			value = Unindexed(value)
		}
		out.Properties[spec.name] = value
	}
	return out, nil
}

func keyFromField(fv reflect.Value) (*Key, error) {
	switch {
	case fv.Type() == keyType:
		k := fv.Interface().(Key)
		return &k, nil
	case fv.Kind() == reflect.Pointer && fv.Type().Elem() == keyType:
		if fv.IsNil() {
			return nil, nil
		}
		k := fv.Interface().(*Key)
		return k, nil
	}
	return nil, fmt.Errorf("datastore: a %q field must be a Key or *Key, got %s", keyTagName, fv.Type())
}

func marshalValue(rv reflect.Value) (Value, error) {
	if !rv.IsValid() {
		return Null(), nil
	}
	// These are structs, so they have to be recognized before the struct case.
	switch rv.Type() {
	case timeType:
		return Time(rv.Interface().(time.Time)), nil
	case keyType:
		return KeyValue(rv.Interface().(Key)), nil
	case valueType:
		return rv.Interface().(Value), nil
	}

	switch rv.Kind() {
	case reflect.Pointer, reflect.Interface:
		if rv.IsNil() {
			return Null(), nil
		}
		return marshalValue(rv.Elem())

	case reflect.String:
		return String(rv.String()), nil

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return IntString(strconv.FormatInt(rv.Int(), 10)), nil

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		// Datastore integers are signed 64-bit. A uint64 above MaxInt64 has no
		// representation, so it is refused rather than wrapped to a negative.
		u := rv.Uint()
		if u > 1<<63-1 {
			return Value{}, fmt.Errorf("uint64 %d exceeds the int64 range Datastore stores", u)
		}
		return IntString(strconv.FormatUint(u, 10)), nil

	case reflect.Float32, reflect.Float64:
		return Float(rv.Float()), nil

	case reflect.Bool:
		return Bool(rv.Bool()), nil

	case reflect.Slice:
		if rv.Type().Elem().Kind() == reflect.Uint8 {
			return Blob(rv.Bytes()), nil
		}
		fallthrough
	case reflect.Array:
		values := make([]Value, 0, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			item, err := marshalValue(rv.Index(i))
			if err != nil {
				return Value{}, err
			}
			values = append(values, item)
		}
		return Array(values...), nil

	case reflect.Struct:
		nested, err := marshalStruct(rv)
		if err != nil {
			return Value{}, err
		}
		if nested.Key != nil {
			return Value{}, fmt.Errorf("an embedded struct must not carry a %q field", keyTagName)
		}
		return Nested(nested), nil

	case reflect.Map:
		return Value{}, fmt.Errorf("maps are not supported; see MarshalEntity")
	}
	return Value{}, fmt.Errorf("unsupported type %s", rv.Type())
}

func unmarshalStruct(e Entity, rv reflect.Value) error {
	rt := rv.Type()
	for _, f := range cachedFields(rt) {
		spec := f.spec
		fv := rv.Field(f.index)
		if !fv.CanSet() {
			continue
		}

		if spec.isKey {
			if err := keyToField(e.Key, fv); err != nil {
				return err
			}
			continue
		}
		value, ok := e.Properties[spec.name]
		if !ok {
			continue
		}
		if err := unmarshalValue(value, fv); err != nil {
			return fmt.Errorf("datastore: field %s: %w", rt.Field(f.index).Name, err)
		}
	}
	return nil
}

func keyToField(key *Key, fv reflect.Value) error {
	switch {
	case fv.Type() == keyType:
		if key != nil {
			fv.Set(reflect.ValueOf(*key))
		}
		return nil
	case fv.Kind() == reflect.Pointer && fv.Type().Elem() == keyType:
		if key == nil {
			fv.Set(reflect.Zero(fv.Type()))
			return nil
		}
		clone := *key
		fv.Set(reflect.ValueOf(&clone))
		return nil
	}
	return fmt.Errorf("datastore: a %q field must be a Key or *Key, got %s", keyTagName, fv.Type())
}

func unmarshalValue(v Value, fv reflect.Value) error {
	// A null clears the field rather than failing. Datastore distinguishes an
	// absent property from a null one, and the Go side of that distinction is
	// "left alone" against "zeroed".
	if v.IsNull() {
		fv.Set(reflect.Zero(fv.Type()))
		return nil
	}

	switch fv.Type() {
	case valueType:
		fv.Set(reflect.ValueOf(v))
		return nil
	case timeType:
		t, ok := v.AsTime()
		if !ok {
			return fmt.Errorf("want a timestamp, got %s", v.Kind())
		}
		fv.Set(reflect.ValueOf(t))
		return nil
	case keyType:
		k, ok := v.AsKey()
		if !ok {
			return fmt.Errorf("want a key, got %s", v.Kind())
		}
		fv.Set(reflect.ValueOf(k))
		return nil
	}

	switch fv.Kind() {
	case reflect.Pointer:
		if fv.IsNil() {
			fv.Set(reflect.New(fv.Type().Elem()))
		}
		return unmarshalValue(v, fv.Elem())

	case reflect.String:
		s, ok := v.AsString()
		if !ok {
			return fmt.Errorf("want a string, got %s", v.Kind())
		}
		fv.SetString(s)
		return nil

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, ok := v.AsInt()
		if !ok {
			return fmt.Errorf("want an integer, got %s", v.Kind())
		}
		if fv.OverflowInt(n) {
			return fmt.Errorf("%d overflows %s", n, fv.Type())
		}
		fv.SetInt(n)
		return nil

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, ok := v.AsInt()
		if !ok {
			return fmt.Errorf("want an integer, got %s", v.Kind())
		}
		if n < 0 {
			return fmt.Errorf("%d does not fit %s", n, fv.Type())
		}
		if fv.OverflowUint(uint64(n)) {
			return fmt.Errorf("%d overflows %s", n, fv.Type())
		}
		fv.SetUint(uint64(n))
		return nil

	case reflect.Float32, reflect.Float64:
		f, ok := v.AsFloat()
		if !ok {
			return fmt.Errorf("want a double, got %s", v.Kind())
		}
		if fv.OverflowFloat(f) {
			return fmt.Errorf("%v overflows %s", f, fv.Type())
		}
		fv.SetFloat(f)
		return nil

	case reflect.Bool:
		b, ok := v.AsBool()
		if !ok {
			return fmt.Errorf("want a boolean, got %s", v.Kind())
		}
		fv.SetBool(b)
		return nil

	case reflect.Slice:
		if fv.Type().Elem().Kind() == reflect.Uint8 {
			b, ok := v.AsBytes()
			if !ok {
				return fmt.Errorf("want a blob, got %s", v.Kind())
			}
			fv.SetBytes(append([]byte(nil), b...))
			return nil
		}
		items, ok := v.AsArray()
		if !ok {
			return fmt.Errorf("want an array, got %s", v.Kind())
		}
		out := reflect.MakeSlice(fv.Type(), len(items), len(items))
		for i, item := range items {
			if err := unmarshalValue(item, out.Index(i)); err != nil {
				return err
			}
		}
		fv.Set(out)
		return nil

	case reflect.Struct:
		nested, ok := v.AsEntity()
		if !ok {
			return fmt.Errorf("want an embedded entity, got %s", v.Kind())
		}
		return unmarshalStruct(nested, fv)
	}
	return fmt.Errorf("unsupported type %s", fv.Type())
}
