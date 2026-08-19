package cbor

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"math"
)

var (
	ErrMalformed       = errors.New("cbor: malformed input")
	ErrTruncated       = errors.New("cbor: truncated input")
	ErrLimitExceeded   = errors.New("cbor: limit exceeded")
	ErrDuplicateMapKey = errors.New("cbor: duplicate map key")
	ErrExtraneousData  = errors.New("cbor: extraneous data after root item")
	ErrUnexpectedToken = errors.New("cbor: unexpected token")
	ErrIntegerOverflow = errors.New("cbor: integer does not fit int64")
	// ErrProfileViolation reports CBOR that is well formed but not legal under
	// the profile it was read or written under.
	ErrProfileViolation = errors.New("cbor: profile violation")
	// ErrFloatRefused reports a float where the configuration carries scaled
	// integers instead. It is a ErrProfileViolation with its own identity,
	// because a float leak is the specific failure a deterministic simulation
	// most needs to be told about.
	ErrFloatRefused = fmt.Errorf("%w: float", ErrProfileViolation)
)

// DecoderOptions controls resource limits and stream behavior. A zero limit
// selects a conservative default. Negative limits are rejected by NewDecoder.
type DecoderOptions struct {
	MaxInputBytes          int64
	MaxNestedLevels        int
	MaxContainerItems      int
	MaxStringBytes         int
	MaxRawMessageBytes     int
	RejectDuplicateMapKeys bool
	Sequence               bool
	// RejectFloats refuses every float on input. Under a profile that carries
	// scaled integers, a float on the wire is a protocol violation rather than
	// a value, and catching it here makes it an error on the receiving side
	// instead of a disagreement about what the message meant.
	RejectFloats bool
}

// maxSliceLen is the largest CBOR argument this package will convert to an int.
// It is math.MaxInt, which is 2^63-1 on a dedicated server and 2^31-1 on a
// 32-bit or js/wasm client.
//
// Naming it is how that width difference stays visible. Every conversion from a
// uint64 argument to an int passes a check against this constant first, so both
// widths refuse the same inputs for the same stated reason instead of one of
// them silently wrapping. A length that survives the check fits an int on the
// machine doing the checking, which is the only machine that will index with it.
const maxSliceLen = math.MaxInt

const (
	defaultMaxInputBytes      = 1 << 20
	defaultMaxNestedLevels    = 32
	defaultMaxContainerItems  = 4096
	defaultMaxStringBytes     = 1 << 20
	defaultMaxRawMessageBytes = 1 << 20
)

// KeyOrder selects which deterministic map key ordering an Encoder emits and
// enforces. RFC 8949 defines two, and they produce different bytes for the same
// map, so the choice is part of the wire contract rather than an internal
// detail. The zero value is LengthFirstKeyOrder.
type KeyOrder uint8

const (
	// LengthFirstKeyOrder sorts shorter encoded keys first and breaks ties
	// bytewise. This is the length-first ordering of RFC 8949 section 4.2.3,
	// which is what CTAP2 canonical CBOR and COSE require.
	LengthFirstKeyOrder KeyOrder = iota
	// BytewiseKeyOrder sorts bytewise lexicographically over the whole encoded
	// key with no length pass. This is RFC 8949 section 4.2.1 Core
	// Deterministic Encoding.
	BytewiseKeyOrder
)

// compare orders two encoded map keys under o.
func (o KeyOrder) compare(a, b []byte) int {
	if o == LengthFirstKeyOrder {
		if c := cmp.Compare(len(a), len(b)); c != 0 {
			return c
		}
	}
	return bytes.Compare(a, b)
}

func (o KeyOrder) String() string {
	if o == BytewiseKeyOrder {
		return "bytewise"
	}
	return "length-first"
}

// EncoderOptions controls deterministic output limits and the map key ordering.
// Zero selects a conservative default. Indefinite-length output is deliberately
// unsupported.
type EncoderOptions struct {
	MaxNestedLevels   int
	MaxContainerItems int
	MaxStringBytes    int
	// KeyOrder selects the map key ordering WriteMap emits and WriteRaw
	// enforces. The zero value keeps the CTAP2 and COSE ordering.
	KeyOrder KeyOrder
}

// RawMessage holds exactly one validated CBOR item when returned by Decoder.
// Callers constructing RawMessage directly should pass it through Validate or
// Encoder.WriteRaw before trusting it.
type RawMessage []byte

type TokenKind uint8

const (
	InvalidToken TokenKind = iota
	UnsignedInteger
	NegativeInteger
	ByteString
	TextString
	StartArray
	EndArray
	StartMap
	EndMap
	Boolean
	Null
	Tag
	Float
)

func (k TokenKind) String() string {
	switch k {
	case UnsignedInteger:
		return "unsigned integer"
	case NegativeInteger:
		return "negative integer"
	case ByteString:
		return "byte string"
	case TextString:
		return "text string"
	case StartArray:
		return "array start"
	case EndArray:
		return "array end"
	case StartMap:
		return "map start"
	case EndMap:
		return "map end"
	case Boolean:
		return "boolean"
	case Null:
		return "null"
	case Tag:
		return "tag"
	case Float:
		return "float"
	default:
		return "invalid"
	}
}

// Token is a typed CBOR token. Argument stores the unsigned argument for
// integers and tags. A NegativeInteger represents -1-Argument, retaining the
// full RFC 8949 range even when it cannot fit in int64.
type Token struct {
	Kind       TokenKind
	Argument   uint64
	Bytes      []byte
	Text       string
	Bool       bool
	Float      float64
	Length     int
	Indefinite bool
}

func (t Token) Int64() (int64, error) {
	switch t.Kind {
	case UnsignedInteger:
		if t.Argument > math.MaxInt64 {
			return 0, ErrIntegerOverflow
		}
		return int64(t.Argument), nil
	case NegativeInteger:
		if t.Argument > math.MaxInt64 {
			return 0, ErrIntegerOverflow
		}
		return -1 - int64(t.Argument), nil
	default:
		return 0, fmt.Errorf("%w: got %s, want integer", ErrUnexpectedToken, t.Kind)
	}
}

// MapEntry is one encoded key/value pair for Encoder.WriteMap. Key and Value
// must each contain exactly one CBOR item. Keys are sorted according to Core
// Deterministic Encoding before they are written.
type MapEntry struct {
	Key   RawMessage
	Value RawMessage
}
