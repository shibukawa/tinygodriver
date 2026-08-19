// Package cbor implements a bounded, reflection-free subset of RFC 8949.
//
// Decoder exposes typed tokens instead of mapping CBOR to arbitrary Go
// values. Encoder writes Core Deterministic Encoding and provides helpers for
// deterministically ordered arrays and maps. Both APIs are intended for
// security-sensitive formats such as WebAuthn authenticator data and COSE_Key.
//
// The package intentionally does not support struct tags, diagnostic notation,
// COSE signing, encryption, or key management.
package cbor
