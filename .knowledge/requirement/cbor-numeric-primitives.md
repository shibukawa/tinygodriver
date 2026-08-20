---
id: requirement:cbor-numeric-primitives
type: requirement
title: Numeric Primitives And Optional Float Refusal
---
Range-checked sized-integer reads, and float refusal as an option rather than a policy. This package has no notion of a scale: a scaled type carries its own encoding through requirement:cbor-codec-interface, and the codec never learns what the scale was.

```yaml
state: shipped 2026-08-19; renamed from cbor-scaled-integer-support on 2026-08-20
renamed_because: >
  the old name said the package supported scaled integers, and it supports no
  such thing. There is no scale field, no conversion, no tag 4, and no fixmath
  dependency. What exists is sized integer reads, which any schema wants, and a
  float switch. The name was the last place a game requirement was still
  claiming to be in the code.
shipped:
  sized_reads: ReadInt8/16/32/64 and ReadUint8/16/32/64 on Reader
  float_refusal: >
    DecoderOptions.RejectFloats refuses a float in Reader.ReadFloat,
    Reader.Skip, Decoder.ReadToken, the ReadRaw scan, and Profile.Validate.
    ErrFloatRefused wraps ErrProfileViolation.
  tag_4: not implemented, which is the recorded refusal
priority: must
on_the_wire: >
  a bare CBOR integer. The scale is carried by the protocol version, never
  transmitted and never negotiated; that is system:ebigentserver's
  fixed-point-on-wire rule.
no_dependency_on_fixmath:
  seam: >
    system:fixmath's F64 is a defined type over int64, so
    requirement:cbor-codec-interface lets it carry its own encoding with neither
    package importing the other
  why_it_matters: >
    a codec bound to one math library cannot serve a game that supplies its own
    core, which fixmath's substitution clause permits
scale_conversion: >
  declared wire scale to and from fixmath's canonical scale belongs to fixmath
  and to generated code, not here
range_checked_reads:
  problem: >
    the encoder writes the shortest form, so a field declared int32 arrives as
    anything from one to five bytes
  needed: >
    ReadInt32, ReadUint16 and the rest of the sized set, where an out-of-range
    value is a protocol error rather than a silent wrap
  today: >
    only ReadInt and ReadUint exist, and ReadInt overflows to ErrIntegerOverflow
    at int64 only
float_is_refused_not_merely_unused:
  today: WriteFloat, MarshalFloat and float decoding are unconditionally available
  required: >
    under the wire profile every one of them is an error, on both sides, so a
    float leak is a caught protocol violation rather than a desync
  including: a float appended by a foreign method, per requirement:cbor-codec-interface
open_tag_4: >
  whether the world profile may carry CBOR tag 4, a decimal fraction of exponent
  and mantissa, to make a scaled value self-describing. Recommendation: no. Tag
  4's exponent is base-10 while these scales are binary, and the protocol
  version already carries what the tag would. Recording the refusal is worth
  more than the option.
out_of_scope: fixed-point arithmetic itself, which is system:fixmath's
```
