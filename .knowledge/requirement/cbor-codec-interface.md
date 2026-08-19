---
id: requirement:cbor-codec-interface
type: requirement
title: Published CBOR Codec Interface
---
A type this package never analyzed must be able to carry its own encoding. The role is `MarshalJSON`'s; the shape is not, because the sibling generator settled the same question the other way.

```yaml
state: shipped 2026-08-19
shipped:
  names: Appender with AppendCBORTo, Decodable with DecodeCBORFrom
  why_not_Decoder: the reader type already holds that name
  foreign_validation: >
    Profile.ValidateAppended(dst, before) checks what a foreign method actually
    wrote, and its tests cover an appended float, an appended indefinite item,
    an implementation that appended nothing, and one that appended two items
  value_constructor: >
    ReaderOver returns a Reader by value, because NewReader's pointer escapes
    and DecodeCBORFrom runs once per field per message
  ecosystem_pair: refused, see refused_second_arm
priority: must
proposed_pair:
  appender: "AppendCBORTo(dst []byte) []byte"
  decoder: "DecodeCBORFrom(data []byte) error, pointer receiver"
append_not_return:
  precedent: >
    tinybind-go settled this on 2026-08-13 as its json-codec-interface
    requirement, naming the pair AppendJSONTo and DecodeJSONFrom; see
    system:tinybind-go
  reason: >
    a byte-returning Marshal allocates once per value and undoes the caller's
    buffer pooling at every nested field. encoding/json/v2 moved to writing into
    an encoder for the same reason. At 60 Hz that is not a stylistic difference;
    see requirement:cbor-allocation-budget.
  naming: match tinybind's pair, so the two codecs read alike
no_error_return: >
  the append method returns no error, matching AppendJSONTo. The append path
  carries no error below this point and adding one would restructure every
  emitted encoder. The doc comment must state the obligation this creates: the
  implementation appends one valid, complete CBOR item for every value of its type.
dispatch: >
  a type assertion, not field walking, so nothing here reaches reflection or
  constrains any TinyGo target
precedence: >
  the method wins over any generated or analyzed encoding for the same type, as
  encoding/json resolves the same conflict. Generating a codec for a type whose
  author wrote one, and then using the generated one, silently emits bytes the
  author did not intend.
codegen_recognition: >
  system:tinybind-go must recognize the interface structurally through go/types,
  as its JSON side already does for foreign kinds, so a field of a foreign type
  is resolved at generation time and emitted as one named call with no runtime branch
composition_at_depth:
  encode: >
    composes for free, since the method appends into the destination the parent
    is already building
  decode_is_blocked: >
    the method takes the field's bytes, so the decoder must hand it one captured
    sub-item. ReadRaw cannot do that. decoder.go:544 refuses whenever rootOpen
    is set, the frame stack is non-empty, or a tag is pending, so it captures a
    whole root item and nothing else. The capture machinery itself,
    scanItemHead, already runs at depth; the requirement is to expose it as a
    bounded nested capture, still inside MaxRawMessageBytes.
the_contract_is_unverifiable: >
  tinybind's JSON side accepts that a hand-written method opts out of the
  module's tag semantics. Here the stakes differ: a foreign method could append
  a float, an indefinite-length item or an unsorted map into a wire-profile
  message, and rule:cbor-determinism-contract would break silently. The
  wire-profile encoder must therefore validate the bytes a foreign method
  appended against the profile, at least under a build tag or an option CI turns
  on. The deterministicParser in encoder.go already does exactly this job for WriteRaw.
refused_second_arm:
  decided: 2026-08-19, by the maintainer; this pair is the only one
  what_was_refused: >
    recognizing MarshalCBOR and UnmarshalCBOR, fxamacker/cbor's spelling, as a
    secondary arm. The recommendation had been to accept it in second position.
  the_decode_half_was_free: >
    UnmarshalCBOR(data []byte) error and DecodeCBORFrom(data []byte) error have
    identical signatures. Refusing it costs nothing either, and one spelling is
    worth more than a synonym.
  the_encode_half_was_not:
    error_contagion: >
      MarshalCBOR returns an error and AppendCBORTo has nowhere to put one, so a
      struct holding one such field cannot implement Appender, and neither can
      its parent, and so on up the type graph. One field changes the encoder
      shape of everything containing it, and system:tinybind-go would have to
      propagate that through the graph and emit two shapes.
    allocation: >
      measured on the same four-field message with two foreign fields: 8.2 ns
      and 0 allocations through the append pair against 46.9 ns and 3 through
      the allocating one. Over a sixteen-entry snapshot, 119.7 ns and 0 against
      893.4 ns and 53. That is the cost
      requirement:cbor-allocation-budget removed.
    determinism: >
      an ecosystem implementation may emit maps in Go map order or emit floats,
      so the arm most likely to violate a profile is the one that would need
      Profile.ValidateAppended turned on, which is the opposite of a fast path
  the_escape_hatch_that_remains: >
    a consumer holding a MarshalCBOR type writes a three-line AppendCBORTo that
    calls it. One allocation per value stays, but the decision to discard the
    error is made explicitly at that call site rather than absorbed silently by
    this package.
open:
  ownership: >
    whether the generated codec belongs to tinybind with this repository
    supplying only the runtime, mirroring the jsonbind split. Assumed
    throughout, not decided.
done_when: >
  a type shaped like system:fixmath's encodes and decodes through this interface
  with no dependency in either direction, and tinybind generates a wire-profile
  codec for a struct holding such a field with no runtime type switch
```
