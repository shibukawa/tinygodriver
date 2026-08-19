// Package cbor implements a bounded, reflection-free subset of RFC 8949.
//
// Decoder exposes typed tokens instead of mapping CBOR to arbitrary Go values.
// Encoder writes deterministic output and provides helpers for deterministically
// ordered arrays and maps. Nothing in either path uses reflection, io.ReadAll,
// or struct tags.
//
// # Map key ordering
//
// RFC 8949 defines two deterministic map key orderings, and they produce
// different bytes for the same map. EncoderOptions.KeyOrder selects between
// them. The zero value is LengthFirstKeyOrder, the section 4.2.3 length-first
// ordering that CTAP2 canonical CBOR and COSE require; BytewiseKeyOrder is the
// section 4.2.1 Core Deterministic Encoding ordering. Callers that care which
// one reaches the wire should set the field rather than inherit it.
//
// # Bounds
//
// Every limit is explicit and every limit is enforced before anything is
// reserved: a declared length never becomes an allocation on its own, because
// the length prefix of a string arrives before its bytes and is attacker
// controlled. DecoderOptions.RejectDuplicateMapKeys additionally validates a
// bounded root item before exposing any of its tokens, at the cost of retaining
// up to MaxRawMessageBytes.
//
// # Intended uses
//
// Security-sensitive formats where preserving signed integer labels and
// rejecting hostile input matter more than mapping arbitrary structs: WebAuthn
// authenticator data and COSE_Key on one side, compact realtime game messages
// on the other.
//
// The package intentionally does not support struct tags, diagnostic notation,
// COSE signing, encryption, key management, or indefinite-length output.
package cbor
