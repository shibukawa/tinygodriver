---
id: rule:cbor-map-key-ordering
type: rule
title: CBOR Map Key Ordering Is Length-First, Not Core Deterministic
---
The encoder sorts map keys shorter-first and then bytewise. That is RFC 8949 section 4.2.3 length-first ordering, which CTAP2 and COSE require, and it is not the section 4.2.1 core deterministic order the package documentation claims.

```yaml
state: resolved 2026-08-19
resolution:
  option: EncoderOptions.KeyOrder, with LengthFirstKeyOrder as the zero value
  default_keeps_passkey: the zero value is the CTAP2 and COSE ordering, unchanged
  world_profile: selects BytewiseKeyOrder
  pinned: >
    the keys -1 and 100 encode as 0x20 and 0x18 0x64, and the two rules order
    them differently. TestKeyOrderingsDisagree asserts both byte strings and
    fails if they ever match.
  enforcement: WriteRaw and Profile.Validate both refuse the other ordering
  documentation: README.md and doc.go now name the order actually emitted
current_behavior:
  where: encoder.go:170; the validating parser enforces the same order on WriteRaw
  order: shorter key first, then bytes.Compare on the encoded key
  correct_for: passkey, since CTAP2 canonical CBOR and COSE are RFC 7049 canonical
documentation_defect:
  claim: README.md and doc.go both say "RFC 8949 Core Deterministic Encoding"
  reality: >
    section 4.2.1 core deterministic sorts bytewise lexicographically on the
    encoded key with no length pass. The two orders produce different bytes for
    the same map.
  cause: >
    the package had one consumer and the WebAuthn framing let the weaker claim
    read as the stronger one; see requirement:cbor-package-move
required:
  - make the ordering an explicit selectable property, length-first or bytewise
  - default it per profile in requirement:cbor-encoding-profiles
  - correct README.md and doc.go to name the order actually emitted
  - pin both orders with tests whose expected bytes disagree
```
