package cbor

import "io"

// Appender is implemented by a type that carries its own CBOR encoding.
//
// AppendCBORTo appends exactly one complete CBOR item to dst and returns the
// extended buffer, the way the strconv.Append and time.AppendFormat families
// do. It takes a destination rather than returning a fresh slice because a
// value returning bytes allocates once per value and undoes the caller's buffer
// pooling at every nested field, which a caller encoding thousands of messages
// a second cannot afford.
//
// It returns no error. The append path below this point carries none, and the
// obligation that creates is on the implementation: for every value of the
// type, it must append one valid, complete CBOR item. Nothing here can check
// that, so an encoder that cares -- one writing under a Profile -- should
// validate the bytes a foreign implementation appended before they reach the
// wire. See Profile.ValidateAppended.
type Appender interface {
	AppendCBORTo(dst []byte) []byte
}

// Decodable is implemented by a type that carries its own CBOR decoding.
//
// DecodeCBORFrom decodes the single CBOR item in data into the receiver, which
// must be a pointer. data holds exactly one item and nothing after it;
// Reader.ReadRaw is what produces it, at whatever depth the field sits.
type Decodable interface {
	DecodeCBORFrom(data []byte) error
}

// The Append family writes one item into a caller-owned buffer. They are the
// primitive layer: no limits, no validation, no error return, because they are
// meant to be the body of a generated encoder in a tight loop.
//
// What they do not check, a Profile checks at the boundary. AppendText will
// happily write invalid UTF-8; Validate is what refuses it.

// AppendUint appends an unsigned integer in its shortest form.
func AppendUint(dst []byte, v uint64) []byte { return appendHead(dst, 0, v) }

// AppendInt appends an integer of either sign in its shortest form.
func AppendInt(dst []byte, v int64) []byte {
	if v >= 0 {
		return appendHead(dst, 0, uint64(v))
	}
	return appendHead(dst, 1, uint64(-(v + 1)))
}

// AppendNegative appends the negative integer -1-arg. It reaches the whole
// encoded negative range, including the half below the int64 floor that
// AppendInt cannot express; the decoder has always been able to read that far.
func AppendNegative(dst []byte, arg uint64) []byte { return appendHead(dst, 1, arg) }

// AppendBytes appends a byte string.
func AppendBytes(dst, v []byte) []byte {
	dst = appendHead(dst, 2, uint64(len(v)))
	return append(dst, v...)
}

// AppendText appends a text string. It does not validate UTF-8.
func AppendText(dst []byte, v string) []byte {
	dst = appendHead(dst, 3, uint64(len(v)))
	return append(dst, v...)
}

// AppendBool appends a boolean.
func AppendBool(dst []byte, v bool) []byte {
	if v {
		return append(dst, 0xf5)
	}
	return append(dst, 0xf4)
}

// AppendNull appends the null value.
func AppendNull(dst []byte) []byte { return append(dst, 0xf6) }

// AppendFloat appends a float in the shortest form that round-trips, which is
// what makes float output deterministic. A wire Profile refuses floats
// outright; this exists for the world profile and for COSE.
func AppendFloat(dst []byte, v float64) []byte { return append(dst, marshalFloat(v)...) }

// AppendTag appends a tag head. The tagged content is the next item appended.
func AppendTag(dst []byte, tag uint64) []byte { return appendHead(dst, 6, tag) }

// AppendArrayHeader appends a definite-length array head for n items, which the
// caller then appends in order.
//
// This is the half of the encoder that was missing. WriteArray takes children
// that are already finished byte slices, so a parent cannot be written until
// every descendant has been built and copied; the cost of that scales with the
// depth of the tree. Appending a header instead lets a nested structure be
// written in one pass into one buffer.
func AppendArrayHeader(dst []byte, n int) []byte { return appendHead(dst, 4, uint64(n)) }

// AppendMapHeader appends a definite-length map head for n key/value pairs.
//
// Nothing sorts the pairs the caller then appends. A streaming writer cannot:
// sorting needs every key at once, which is the shape this exists to avoid.
// Generated code knows its field set before it runs and can emit the pairs in
// order; hand-written callers that need the sort should keep using
// Encoder.WriteMap, and either way Validate under a Profile is what proves the
// result.
func AppendMapHeader(dst []byte, n int) []byte { return appendHead(dst, 5, uint64(n)) }

// AppendRaw appends an item that is already encoded, without validating it.
func AppendRaw(dst []byte, raw RawMessage) []byte { return append(dst, raw...) }

// MarshalNegative returns the negative integer -1-arg as a RawMessage, reaching
// the range MarshalInt cannot. See AppendNegative.
func MarshalNegative(arg uint64) RawMessage { return RawMessage(appendHead(nil, 1, arg)) }

// WriteNegative writes the negative integer -1-arg. See AppendNegative.
func (e *Encoder) WriteNegative(arg uint64) error { return e.write(appendHead(nil, 1, arg)) }

// Reset points the Encoder at a new writer, keeping its options, so a session
// can hold one encoder instead of allocating one per message.
func (e *Encoder) Reset(w io.Writer) error {
	if w == nil {
		return errorsf("nil writer")
	}
	e.w = w
	return nil
}
