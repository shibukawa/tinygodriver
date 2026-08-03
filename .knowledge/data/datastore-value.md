---
id: data:datastore-value
type: data
title: Datastore Value And Entity
---
One property in its wire form, encoded by hand so the type set is explicit, following the pattern data:dynamodb-attribute-value established. The proto3 JSON mapping makes this a closer parallel than it looks: both are a one-member union with int64 carried as text.

```yaml
type: |
  type Value struct {
      Null      bool
      Bool      *bool
      Integer   *string     // int64, text on the wire, per proto3 JSON
      Double    *float64
      Timestamp *time.Time  // RFC 3339 on the wire
      Key       *Key
      String    *string
      Blob      []byte      // base64 on the wire
      GeoPoint  *LatLng
      Entity    *Entity     // a nested entity, no key
      Array     []Value

      ExcludeFromIndexes bool   // not part of the union
  }
  type Entity struct { Key *Key; Properties map[string]Value }
  type Key struct { Namespace string; Path []PathElement }
  type PathElement struct { Kind string; ID int64; Name string }   // exactly one of ID, Name
  type LatLng struct { Latitude, Longitude float64 }
constructors:
  - "func String(v string) Value"
  - "func Int[T Integer](v T) Value"
  - "func IntString(v string) Value    // beyond what int64 parses, or to avoid a conversion"
  - "func Float(v float64) Value"
  - "func Bool(v bool) Value"
  - "func Time(v time.Time) Value"
  - "func Blob(v []byte) Value"
  - "func KeyValue(k Key) Value"
  - "func GeoPoint(lat, lng float64) Value"
  - "func Nested(e Entity) Value"
  - "func Array(vs ...Value) Value"
  - "func Null() Value"
  - "func Unindexed(v Value) Value   // sets ExcludeFromIndexes, composes with any of the above"
accessors: >
  AsString, AsInt, AsFloat, AsBool, AsTime, AsBytes, AsKey, AsArray, AsEntity,
  AsNumber for the stored text, IsNull, and Kind, matching the shape callers
  already know from data:dynamodb-attribute-value
codec:
  union: >
    MarshalJSON emits exactly one member and UnmarshalJSON accepts exactly one.
    Zero members is ErrEmptyValue, more than one is ErrAmbiguousValue. This is
    the same rule as the DynamoDB codec, and here it is not a house style but
    the proto3 encoding of a oneof.
  exclude_from_indexes: >
    the one field outside the union, so it is emitted alongside the member
    rather than instead of it
  integers: >
    proto3 JSON writes int64 as a string, the same trick DynamoDB uses for N and
    for the same reason. Integer stays text end to end; conversion happens in
    AsInt where the caller has chosen a Go type.
  doubles: >
    a real JSON number, unlike Integer. A value that arrives as doubleValue and
    one that arrives as integerValue are different types to Datastore, and
    collapsing them would break sort order and equality filters.
  timestamps: >
    RFC 3339 with up to nanosecond precision. Datastore stores microseconds, so
    a round trip truncates; the constructor does not hide that.
  blobs: base64 standard encoding, the proto3 bytes mapping
  keys_in_values: >
    a keyValue carries a full partitionId, so encoding one needs the project and
    namespace the client was built with, not just the path
  nested_entity: >
    an entityValue has no key. Encoding an Entity with a key inside a value is
    rejected rather than silently dropped.
  empty_string: a valid value, distinct from Null and from an absent property
  absent_vs_null: >
    a property missing from the map and a property set to Null are different
    things to a query filter, and the map representation keeps them different
key_encoding:
  path: "at least one element; the last is the entity, earlier ones are ancestors"
  incomplete: >
    a PathElement with neither ID nor Name is an incomplete key, legal only in
    an insert or an allocateIds call, where the server fills it in
  partition: >
    partitionId is added by the client at encode time from its project and
    namespace, so a Key value stays portable inside a program
struct_mapping:
  optional: "func MarshalEntity(v any) (Entity, error) and UnmarshalEntity(Entity, any) error"
  tag: >
    datastore, matching the cloud.google.com/go/datastore spelling so examples
    port over, the same courtesy dynamodbav gets
  options: "`,noindex` and `,omitempty` on the tag; `-` skips"
  cost: reflection, opt-in for the same reason as decision:dynamodb-json-codec
verified_under_tinygo: >
  not separately measured. requirement:dynamodb-driver-validation established
  encoding/json and reflect under tinygo, and this is the same machinery over a
  different schema.
wire_reference: system:google-datastore
used_by: api:datastore-client
counterpart: data:dynamodb-attribute-value
