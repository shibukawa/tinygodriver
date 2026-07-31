---
id: data:dynamodb-attribute-value
type: data
title: DynamoDB Attribute Value
---
One attribute in its wire form, encoded by hand rather than by reflection, so the type set is explicit and the JSON is exactly what DynamoDB expects.

```yaml
type: |
  type AttributeValue struct {
      S    *string                    // string
      N    *string                    // number, kept as text to preserve precision
      B    []byte                     // binary, base64 on the wire
      BOOL *bool
      NULL bool
      L    []AttributeValue           // list
      M    map[string]AttributeValue  // map
      SS   []string                   // string set
      NS   []string                   // number set
      BS   [][]byte                   // binary set
  }
constructors:
  - "func S(v string) AttributeValue"
  - "func N[T Number](v T) AttributeValue"
  - "func NString(v string) AttributeValue   // for more precision than float64 holds"
  - "func B(v []byte) AttributeValue"
  - "func Bool(v bool) AttributeValue"
  - "func Null() AttributeValue"
  - "func List(vs ...AttributeValue) AttributeValue"
  - "func Map(m map[string]AttributeValue) AttributeValue"
  - "func SS(vs ...string) AttributeValue, and NS, BS for the other sets"
accessors:
  - "func (a AttributeValue) AsString() (string, bool)"
  - "func (a AttributeValue) AsInt() (int64, bool)"
  - "func (a AttributeValue) AsFloat() (float64, bool)"
  - "func (a AttributeValue) AsBytes() ([]byte, bool)"
  - "func (a AttributeValue) AsBool() (bool, bool)"
  - "func (a AttributeValue) AsNumber() (string, bool)   // lossless, the stored text"
  - "func (a AttributeValue) AsList() ([]AttributeValue, bool)"
  - "func (a AttributeValue) AsMap() (map[string]AttributeValue, bool)"
  - "func (a AttributeValue) IsNull() bool"
  - "func (a AttributeValue) Kind() Kind"
state: shipped 2026-07-31
codec:
  marshal: >
    MarshalJSON emits exactly one member. Zero fields set is ErrEmptyAttribute
    and more than one is ErrAmbiguousAttribute, rather than the first-set-wins
    rule this concept originally proposed: the wire form carries one member, so
    there is no correct choice to make, and picking one silently would encode
    something the caller did not mean.
  unmarshal: >
    UnmarshalJSON reads exactly one member and rejects an unknown tag, a member
    count other than one, and {"NULL":false}, which is not a value DynamoDB
    sends and would decode to an attribute with nothing set
  numbers: >
    N stays a string end to end. DynamoDB numbers carry 38 digits of precision,
    which float64 cannot hold, so conversion happens only in AsInt and AsFloat
    where the caller has chosen a Go type.
  empty_string: valid since 2020 and passed through unchanged
  zero_value: an AttributeValue with no field set is an encoding error, not NULL
struct_mapping:
  optional: "func MarshalItem(v any) (Item, error) and UnmarshalItem(Item, any) error"
  tag: dynamodbav, matching the aws-sdk-go-v2 spelling so examples port over
  cost: reflection; see decision:dynamodb-json-codec
verified_under_tinygo: requirement:dynamodb-driver-validation
used_by: api:dynamodb-client
