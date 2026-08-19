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
