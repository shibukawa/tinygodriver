# encoding/cbor

A bounded, reflection-free RFC 8949 codec for TinyGo and Go. It preserves signed
integer labels, rejects hostile input, and never maps arbitrary structs, which
suits WebAuthn authenticator data and COSE keys as well as compact realtime
message formats.

## Supported subset

- unsigned and negative integers, including the full encoded negative range on decode
- byte and UTF-8 text strings
- arrays and maps
- booleans, null, tags, and IEEE 754 half/single/double input
- definite and indefinite-length input
- deterministic output, with a selectable map key ordering
- incremental `io.Reader` decoding with input, nesting, container, string, and
  retained-raw-message limits
- optional duplicate map key rejection; that mode validates a bounded root item
  before exposing its tokens
- explicit sequence mode; normal mode rejects bytes after one root item

## Two decoders, two encoders

`Decoder` reads incrementally from an `io.Reader` and copies what it returns.
Use it for input that arrives over a connection, or that is larger than memory
should hold at once.

`Reader` reads from a byte slice already in memory. It borrows strings instead
of copying them, skips an item without allocating it, and captures a sub-item at
any depth — none of which an incremental decoder can do. Reuse one per
connection with `Reset`.

On the writing side, `Encoder` writes whole items to an `io.Writer`, and the
`Append*` functions write into a caller-owned `[]byte`. `AppendArrayHeader` and
`AppendMapHeader` let a nested structure be written in one pass, rather than
requiring every child to be a finished `RawMessage` first.

```go
buf = cbor.AppendArrayHeader(buf[:0], 4)
buf = cbor.AppendUint(buf, tick)
buf = moveX.AppendCBORTo(buf)   // a type carrying its own encoding
buf = cbor.AppendUint(buf, buttons)
```

## Carrying your own encoding

A type says how it encodes by implementing one of:

```go
type Appender  interface { AppendCBORTo(dst []byte) []byte }
type Decodable interface { DecodeCBORFrom(data []byte) error } // pointer receiver
```

They append into the destination rather than returning bytes, so a nested field
costs no allocation of its own. `AppendCBORTo` returns no error; the obligation
that creates is that it appends exactly one valid, complete item. Nothing in the
type system can hold a foreign implementation to that, so `Profile.ValidateAppended`
checks what it actually wrote.

## Nesting

`Skip`, `ReadRaw` and `Profile.Validate` drive their own recursion and bound it
by `MaxNestedLevels`. `ReadArrayHeader` and `ReadMapHeader` do not — they read
one head and return, so the walk over the container is your loop and its depth
is yours to bound. `Decoder` differs here: it keeps a frame stack and refuses a
container past the limit from `ReadToken`.

That is deliberate. A frame stack is the state `Reader` does not keep, and
keeping one would cost an allocation per reader in the path that exists to have
none. For untrusted input, validate first:

```go
if err := cbor.World().Validate(data); err != nil { return err }
// now the bytes are known to be legal under the profile's bounds
```

Foreign types compose to any depth on both sides at zero allocation: encoding
because each `AppendCBORTo` appends into the buffer its parent is already
building, decoding because `ReadRaw` hands each one its own bytes wherever the
field sits.

### How deep is deep

`MaxNestedLevels` defaults to 10000 — the same number `encoding/json` uses, and
for the same reason. It is a **stack safety net, not a budget**: both profiles
take it, and no schema should ever meet it.

The net is not optional here. Every walk in this package recurses, and measured
on darwin/arm64:

| | survives | fails at | how |
|---|---|---|---|
| host Go | ~1,000,000 levels | ~2,000,000 | `fatal error: stack overflow` |
| **TinyGo** | ~46,500 levels | ~47,000 | **bare SIGSEGV, no message** |

TinyGo does not detect stack exhaustion. That is why a limit exists even though
a caller should never see it. About 180 bytes of stack go per level, so a target
with a small stack wants a smaller number.

Narrow it deliberately if you want a structural check on a known schema:

```go
tight := cbor.Wire().WithMaxNestedLevels(12)
```

`MaxNestedLevels` counts nested containers, and **a tag is one of them** — a tag
over an array is two levels, not one.

The two ways of reading count differently, and the difference decides what bound
you need:

- `Validate` walks the whole document, so depths **add**. An envelope wrapping a
  message costs the envelope's depth plus the message's.
- `ReadRaw` measures a captured item **from zero**, so decoding an envelope field
  by field and handing each payload on as raw bytes costs the **larger** of the
  two, not their sum.

This matters for anything that carries a subtree of a document: a patch, a delta,
a log entry quoting a message. Such a document is always deeper than the document
it describes — for the shape `[base, [[op, [path...], value], ...]]` it is exactly
three levels deeper. **A profile whose bound only just fits its messages will
refuse every patch of one**, and that shows up the first time a delta is
generated, not before.

Derive an envelope bound instead of guessing one:

```go
envelope := cbor.World().WithMaxNestedLevels(cbor.World().MaxNestedLevels() + 3)
```

Neither profile bounds nesting to anything a schema would meet, so this only
matters if you narrow one. `MaxNestedLevels()`, `MaxContainerItems()` and
`MaxInputBytes()` report what a profile carries, so the arithmetic can be
written down rather than assumed.

## Profiles

A `Profile` is a named restriction, so both ends name the same shape instead of
assembling limits by hand.

- `Wire()` — fixed-order arrays, no maps, no tags, no floats, no indefinite
  lengths, small bounds. Field names never appear, which is what makes it small
  and why a version mismatch has to be settled before any message is read.
- `World()` — maps, optional fields and tags, bytewise key order, bounds sized
  for snapshots rather than ticks.

Both refuse floats: numerics are scaled integers, so a float is a protocol
violation rather than a value, and it is refused at encode **and** at decode.

```go
if err := cbor.Wire().Validate(data); err != nil { /* not a wire message */ }
```

`Profile.Validate` answers the question without decoding the item into anything.

## Cost

A fixed-shape wire message, encoded and decoded through a reused buffer and a
reused `Reader`:

| | ns/op | allocs/op |
|---|---|---|
| encode, `Append*` | 9.2 | 0 |
| encode, `WriteArray` over `RawMessage` children | 100.4 | 5 |
| decode, `Reader` | 42.4 | 0 |
| decode, `Decoder` over an `io.Reader` | 168.0 | 7 |
| `Wire().Validate` | 23.9 | 0 |
| `World().Validate` | 90.0 | 0 |
| `Skip` an unknown field | 24.2 | 0 |

Measured on darwin/arm64 with `go test -bench . -benchtime 3s`. The point is the
allocation column: the steady state of a tick loop has to be free of it.

## Errors

Every refusal wraps one of the package sentinels, so `errors.Is` works as
before, and adds where it happened:

```
cbor: malformed input: reserved additional information 28 (at byte 5, at [2][1])
```

`errors.As` reaches the `*cbor.Error` for the offset and the route. Nothing
tracks a route while decoding succeeds — it is built afterwards by walking the
input again — so a message that decodes cleanly pays nothing for this.

## Map key ordering

RFC 8949 defines two deterministic orderings and they disagree. `EncoderOptions.KeyOrder`
selects one:

- `LengthFirstKeyOrder` (the zero value) sorts shorter encoded keys first, then
  bytewise. This is §4.2.3, and it is what CTAP2 canonical CBOR and COSE require.
- `BytewiseKeyOrder` sorts bytewise over the whole encoded key. This is §4.2.1
  Core Deterministic Encoding.

The keys `-1` and `100` encode as `20` and `1864`, and the two rules order them
differently. `WriteRaw` enforces whichever ordering the encoder was configured
with, so a raw item canonical under one is rejected under the other.

## Intentionally unsupported

- reflection-based struct or arbitrary Go value mapping
- CBOR diagnostic notation
- unassigned simple values and CBOR `undefined`
- indefinite-length encoding (it cannot be deterministic)
- COSE signing, encryption, or key management

For untrusted maps, enable `DecoderOptions.RejectDuplicateMapKeys`. This mode
may retain one root item up to `MaxRawMessageBytes` so duplicate keys can be
checked before application code observes any field.
