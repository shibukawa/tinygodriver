---
id: requirement:cbor-package-move
type: requirement
title: CBOR Package Moves In From Popcorn Web
---
`popcornweb/contrib/cbor` becomes a package of this repository. It is a bounded, reflection-free RFC 8949 subset with no web-framework surface, and it now has three consumers rather than one.

```yaml
priority: must
state: shipped 2026-08-19 at encoding/cbor
shipped:
  tests: 140 pass under standard Go and under TinyGo 0.41.1
  fuzz: >
    three targets behind !tinygo. FuzzValidate came with the package;
    FuzzReaderSurface and FuzzRoundTripUnderProfile cover the byte-slice
    surface, and the first of those asserts agreement rather than absence of a
    panic: a profile that accepts an item must describe one Skip consumes
    exactly, and a captured raw item must be a prefix of its input. 16 million
    executions, no failures.
  passkey: >
    passes against the moved package with contrib/cbor deleted and the import
    path changed, verified in a throwaway worktree of popcornweb
  imports: >
    bytes, cmp, encoding/binary, errors, fmt, io, math, slices, unicode/utf8.
    sort and strconv are gone; cmp and slices replaced them.
  size: 3038 non-test lines, up from 1451; 1650 test lines, up from 467
  remaining: >
    deleting popcornweb/contrib/cbor is the other repository's change and has
    not been made here
state: specification as of 2026-08-19; no code moved
source:
  module: github.com/shibukawa/popcornweb, renamed from popcornwave on 2026-08-18
  path: contrib/cbor
  framework: system:popcornwave
destination: decision:cbor-import-path
license: Apache-2.0 on both sides, so the move raises no licensing question
size:
  total: 1916 lines across ten files, 1451 of them non-test
  decoder.go: 864 lines; incremental io.Reader decoder, token stream, typed reads, ReadRaw, Validate
  encoder.go: 437 lines; deterministic writer, Marshal* raw constructors, a validating parser
  types.go: 138 lines; options, errors, Token, RawMessage, MapEntry
  tests: 467 lines; RFC 8949 Appendix A vectors, allocation bounds, fuzz behind !tinygo, examples
no_new_dependencies: >
  imports bytes, encoding/binary, fmt, io, math, sort, strconv and unicode/utf8
  only; requirement:cbor-decoder-defects removes sort
consumers:
  passkey: popcornweb/contrib/passkey; attestation objects and COSE_Key, CTAP2 canonical CBOR
  realtime: system:ebigentserver; player input, game events, state deltas, wire profile
  world: system:ebigentserver; snapshots and episode logs, world profile
why_it_cannot_stay: >
  the game framework would depend on a web framework for its wire format, which
  system:ebigentserver's independent-from-popcorn-wave decision rules out
dependency_direction:
  today: popcornweb already requires tinygodriver v1.2.4, so consuming from the new home adds no edge
  invariant: tinygodriver must never import popcornweb
callers_to_reimport:
  - contrib/passkey/parse.go:14
  - contrib/passkey/passkeytest/encode.go:10
  - contrib/passkey/passkey_test.go:22
  - contrib/cbor/example_test.go:7
old_path: >
  deleted. A type-alias shim at popcornweb/contrib/cbor is acceptable for one
  release if external callers exist; it must be marked deprecated and carry a
  removal date rather than an intention.
tests_move_unchanged: >
  the allocation bound, the Appendix A vectors and the !tinygo fuzz target are
  the regression evidence for every change requirement:cbor-codec-interface and
  its siblings then make, so they must pass in the new home first
webauthn_contract_frozen: >
  passkey is the one production consumer. It must pass on the moved package with
  no change beyond the import path before any extension work starts.
documentation_rewrite: >
  README.md and doc.go say the package is designed for WebAuthn authenticator
  data and COSE keys. After the move it is equally a game wire codec, and that
  framing already produced one documentation error; see rule:cbor-map-key-ordering.
done_when:
  - passkey passes its existing tests against the moved package, import path change only
  - Appendix A vectors, allocation bound and fuzz target pass in the new home
  - popcornweb/contrib/cbor is gone, or is a deprecated alias with a removal date
  - tinygodriver still imports nothing from popcornweb
  - a TinyGo build of both consumers links and runs
```
