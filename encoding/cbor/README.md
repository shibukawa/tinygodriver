# contrib/cbor

`contrib/cbor` is a bounded, reflection-free RFC 8949 codec for TinyGo and Go.
It is designed for WebAuthn authenticator data and COSE keys, where preserving
signed integer labels and rejecting hostile input are more useful than mapping
arbitrary structs.

## Supported subset

- unsigned and negative integers, including the full encoded negative range
- byte and UTF-8 text strings
- arrays and maps
- booleans, null, tags, and IEEE 754 half/single/double input
- definite and indefinite-length input
- RFC 8949 Core Deterministic Encoding output
- incremental `io.Reader` decoding with input, nesting, container, string, and
  retained-raw-message limits
- optional duplicate map key rejection; security mode validates a bounded root
  item before exposing its tokens
- explicit sequence mode; normal mode rejects bytes after one root item

The decoder exposes tokens and typed readers (`ReadInt`, `ReadBytes`,
`ReadArray`, and related methods). It does not use reflection or `io.ReadAll`.
`ReadRaw` provides bounded deferred decoding. The encoder provides typed writers
and deterministic `WriteArray` / `WriteMap`; map keys are sorted before output.

## Intentionally unsupported

- reflection-based struct or arbitrary Go value mapping
- CBOR diagnostic notation
- unassigned simple values and CBOR `undefined`
- indefinite-length encoding (it cannot be deterministic)
- COSE signing, encryption, or key management

For untrusted maps, enable `DecoderOptions.RejectDuplicateMapKeys`. This mode
may retain one root item up to `MaxRawMessageBytes` so duplicate keys can be
checked before application code observes any field.
