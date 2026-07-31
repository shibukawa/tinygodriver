---
id: decision:dynamodb-json-codec
type: decision
title: Attribute Values Are Encoded By Hand, Reflection Is Opt-In
---
`data:dynamodb-attribute-value` carries its own `MarshalJSON` and `UnmarshalJSON`; the struct-tag marshaler is a separate, optional entry point.

```yaml
state: proposed
proposed_on: 2026-07-30
measured_with: tinygo 0.41.1, see requirement:dynamodb-driver-validation
finding: >
  both encoding/json and reflect work under tinygo, so this is a cost decision
  rather than a feasibility one
chosen:
  core: explicit codec on the AttributeValue type, no reflection on the hot path
  optional: MarshalItem and UnmarshalItem over reflect, in their own file
  effect: >
    a program that only uses AttributeValue constructors does not link the
    reflection marshaler, which matters for a firmware-sized binary
rejected:
  attributevalue_dependency: >
    aws-sdk-go-v2/feature/dynamodbav pulls the service types, which pull the
    transport that does not build; see decision:no-aws-sdk-go-v2
  reflection_only_api: >
    convenient, but it makes every caller pay for reflect and hides the
    number-precision question that data:dynamodb-attribute-value answers
  map_string_any_api: >
    an untyped map cannot express the difference between a number and a numeric
    string, which is exactly the distinction DynamoDB stores
consequences:
  - the type set is visible in the API, so an unsupported type is a compile error
  - N stays text, so a 38-digit number survives a round trip
  - MarshalItem can be dropped from a build without changing the core API
  - binary size effect is measured on the example, per requirement:dynamodb-client-scope
