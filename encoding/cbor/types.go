package cbor

import (
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
}

const (
	defaultMaxInputBytes      = 1 << 20
	defaultMaxNestedLevels    = 32
	defaultMaxContainerItems  = 4096
	defaultMaxStringBytes     = 1 << 20
	defaultMaxRawMessageBytes = 1 << 20
)

// EncoderOptions controls deterministic output limits. Zero selects a
// conservative default. Indefinite-length output is deliberately unsupported.
type EncoderOptions struct {
	MaxNestedLevels   int
	MaxContainerItems int
	MaxStringBytes    int
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
