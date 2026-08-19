package cbor

import (
	"fmt"
)

// A Profile is a named restriction on what CBOR is legal, so a caller names the
// shape it wants rather than assembling limits by hand and hoping both ends
// assembled the same ones.
//
// A Profile only enforces. It never infers which profile a message should be
// read under; that belongs to the schema and to the caller.
type Profile struct {
	name               string
	maxNestedLevels    int
	maxContainerItems  int
	maxStringBytes     int
	maxInputBytes      int64
	maxRawMessageBytes int
	keyOrder           KeyOrder
	allowMaps          bool
	allowTags          bool
	allowFloats        bool
	allowIndefinite    bool
	allowTextKeys      bool
}

// Wire returns the compact profile for realtime messages.
//
// A struct encodes as a fixed-order array with no field names, which is what
// makes it small and what makes a version mismatch undetectable from the bytes
// alone: there is nothing in the message to disagree about a field with. That
// is why the protocol version has to be agreed before any message is read,
// rather than negotiated per field.
//
// Numerics are scaled integers. Floats are refused outright, at encode and at
// decode, so a float that reaches the wire is a caught protocol violation
// rather than two peers drifting apart.
//
// Nesting is not restricted beyond the package's stack safety net. A wire
// message is shallow in practice, but bounding that would make every envelope
// over one -- a patch, a delta, a log entry quoting a message -- something the
// caller has to do arithmetic about, for no protection the restriction on maps,
// tags and floats does not already give.
func Wire() Profile {
	return Profile{
		name:               "wire",
		maxNestedLevels:    defaultMaxNestedLevels,
		maxContainerItems:  1024,
		maxStringBytes:     4096,
		maxInputBytes:      64 << 10,
		maxRawMessageBytes: 8 << 10,
		allowMaps:          false,
		allowTags:          false,
		allowFloats:        false,
		allowIndefinite:    false,
		allowTextKeys:      false,
	}
}

// World returns the evolvable profile for snapshots and episode logs.
//
// It admits maps, optional fields and tags, so a schema can grow, and it holds
// map keys in bytewise order -- RFC 8949 section 4.2.1 Core Deterministic
// Encoding -- rather than the length-first order CTAP2 requires, because
// nothing here is COSE.
//
// Its bounds are larger than the wire profile's because a snapshot is larger
// than a tick. In particular MaxRawMessageBytes is set deliberately: duplicate
// key detection retains a whole root item, and the 1 MiB default would refuse
// snapshots this profile is meant to carry.
func World() Profile {
	return Profile{
		name:               "world",
		maxNestedLevels:    defaultMaxNestedLevels,
		maxContainerItems:  1 << 16,
		maxStringBytes:     4 << 20,
		maxInputBytes:      64 << 20,
		maxRawMessageBytes: 64 << 20,
		keyOrder:           BytewiseKeyOrder,
		allowMaps:          true,
		allowTags:          true,
		allowFloats:        false,
		allowIndefinite:    false,
		allowTextKeys:      true,
	}
}

// Name reports the profile's name, which appears in its refusals.
func (p Profile) Name() string { return p.name }

// MaxNestedLevels reports the nesting bound, so a caller wrapping messages of
// this profile in an envelope can derive its own bound from it rather than
// guess one. See the note on Validate for the arithmetic.
func (p Profile) MaxNestedLevels() int { return p.maxNestedLevels }

// MaxContainerItems reports the container bound.
func (p Profile) MaxContainerItems() int { return p.maxContainerItems }

// MaxInputBytes reports the input bound.
func (p Profile) MaxInputBytes() int64 { return p.maxInputBytes }

// AllowingFloats returns a copy of p that admits floats. A profile carrying
// scaled integers should not need it; it exists so that a caller who has
// decided otherwise says so at the call site rather than by editing a preset.
func (p Profile) AllowingFloats() Profile { p.allowFloats = true; return p }

// WithMaxInputBytes returns a copy of p with a different input bound.
func (p Profile) WithMaxInputBytes(n int64) Profile { p.maxInputBytes = n; return p }

// WithMaxNestedLevels returns a copy of p with a different nesting bound.
func (p Profile) WithMaxNestedLevels(n int) Profile { p.maxNestedLevels = n; return p }

// DecoderOptions returns the limits this profile implies, for NewDecoder or
// NewReader.
func (p Profile) DecoderOptions() DecoderOptions {
	return DecoderOptions{
		MaxInputBytes:          p.maxInputBytes,
		MaxNestedLevels:        p.maxNestedLevels,
		MaxContainerItems:      p.maxContainerItems,
		MaxStringBytes:         p.maxStringBytes,
		MaxRawMessageBytes:     p.maxRawMessageBytes,
		RejectDuplicateMapKeys: p.allowMaps,
		RejectFloats:           !p.allowFloats,
	}
}

// EncoderOptions returns the limits and key ordering this profile implies.
func (p Profile) EncoderOptions() EncoderOptions {
	return EncoderOptions{
		MaxNestedLevels:   p.maxNestedLevels,
		MaxContainerItems: p.maxContainerItems,
		MaxStringBytes:    p.maxStringBytes,
		KeyOrder:          p.keyOrder,
	}
}

// NewReader returns a Reader over data with this profile's limits.
func (p Profile) NewReader(data []byte) (*Reader, error) {
	return NewReader(data, p.DecoderOptions())
}

// ReaderOver returns a Reader by value with this profile's limits. See
// ReaderOver.
func (p Profile) ReaderOver(data []byte) Reader {
	return ReaderOver(data, p.DecoderOptions())
}

// Validate reports whether data is exactly one item legal under p. It answers
// the question without decoding the item into anything, which is what makes it
// usable as a boundary check on bytes from somewhere else.
//
// # Depth arithmetic
//
// Neither profile bounds nesting to anything a schema would meet: both take the
// package default, which is a stack safety net rather than a budget. What
// follows matters only for a caller who narrows it deliberately.
//
// Validate walks the whole document, so nesting adds up: an envelope that wraps
// a message costs the envelope's depth plus the message's. A patch or delta
// carrying a subtree of a document is therefore deeper than the document, and a
// profile whose bound only just fits its messages will refuse patches of them.
//
// Reading does not add up the same way. Reader.ReadRaw measures the captured
// item from zero, so decoding an envelope field by field and handing each
// payload on as raw bytes costs the larger of the two depths rather than their
// sum. An envelope needing 7 as one document decodes under a bound of 6 that
// way, and Validate still refuses it.
//
// Where the two must agree -- validating untrusted bytes before decoding them --
// the bound has to cover the sum. Derive it rather than guess it:
//
//	envelope := World().WithMaxNestedLevels(World().MaxNestedLevels() + 3)
//
// Three is what a patch of the shape [base, [[op, [path...], value], ...]]
// adds over the value it carries.
func (p Profile) Validate(data []byte) error {
	v := profileValidator{p: p, r: Reader{data: data, opts: p.DecoderOptions()}}
	if err := v.item(0); err != nil {
		return err
	}
	if v.r.off != len(data) {
		return v.r.at(v.r.off, ErrExtraneousData)
	}
	return nil
}

// ValidateAppended checks the item a foreign AppendCBORTo just wrote, given the
// buffer and the length it had before the call.
//
// A type that carries its own encoding cannot be made to honour a profile by
// the type system: nothing stops it appending a float, an indefinite-length
// item, or an unsorted map into a message that must not contain one. Nothing
// downstream would notice either, because the bytes are well-formed CBOR --
// they are simply not the CBOR this profile promised. Running this after the
// call is what turns that from a silent divergence into an error.
func (p Profile) ValidateAppended(dst []byte, before int) error {
	if before < 0 || before > len(dst) {
		return errorsf("ValidateAppended: length before the call is out of range")
	}
	appended := dst[before:]
	if len(appended) == 0 {
		return &Error{Offset: int64(before), Err: fmt.Errorf("%w: appended nothing, want one item", ErrTruncated)}
	}
	if err := p.Validate(appended); err != nil {
		if e, ok := err.(*Error); ok {
			e.Offset += int64(before)
		}
		return err
	}
	return nil
}

func (p Profile) refuse(what string) error {
	return fmt.Errorf("%w: the %s profile does not permit %s", ErrProfileViolation, p.name, what)
}

// profileValidator walks one item, checking it against a profile as it goes.
type profileValidator struct {
	p Profile
	r Reader
}

func (v *profileValidator) item(depth int) error {
	start := v.r.off
	if depth > v.p.maxNestedLevels {
		return v.r.at(start, fmt.Errorf("%w: nesting depth", ErrLimitExceeded))
	}
	major, ai, arg, indefinite, err := v.r.head()
	if err != nil {
		return v.r.at(start, err)
	}
	if indefinite && !v.p.allowIndefinite {
		return v.r.at(start, v.p.refuse("indefinite lengths"))
	}
	switch major {
	case 0, 1:
		return nil
	case 2, 3:
		if arg > uint64(v.p.maxStringBytes) || arg > uint64(maxSliceLen) {
			return v.r.at(start, fmt.Errorf("%w: string bytes", ErrLimitExceeded))
		}
		if _, err := v.r.take(int(arg)); err != nil {
			return v.r.at(start, err)
		}
		return nil
	case 4:
		if arg > uint64(v.p.maxContainerItems) {
			return v.r.at(start, fmt.Errorf("%w: container items", ErrLimitExceeded))
		}
		for i := uint64(0); i < arg; i++ {
			if err := v.item(depth + 1); err != nil {
				return err
			}
		}
		return nil
	case 5:
		if !v.p.allowMaps {
			return v.r.at(start, v.p.refuse("maps"))
		}
		return v.mapItem(depth, start, arg)
	case 6:
		if !v.p.allowTags {
			return v.r.at(start, v.p.refuse("tags"))
		}
		return v.item(depth + 1)
	default:
		switch ai {
		case 20, 21, 22:
			return nil
		case 25, 26, 27:
			if !v.p.allowFloats {
				return v.r.at(start, v.p.refuse("floats"))
			}
			return nil
		default:
			return v.r.at(start, fmt.Errorf("%w: unsupported simple value %d", ErrMalformed, ai))
		}
	}
}

func (v *profileValidator) mapItem(depth int, start int, pairs uint64) error {
	if pairs > uint64(v.p.maxContainerItems) {
		return v.r.at(start, fmt.Errorf("%w: map pairs", ErrLimitExceeded))
	}
	var prev []byte
	for i := uint64(0); i < pairs; i++ {
		keyStart := v.r.off
		keyKind, err := v.r.Peek()
		if err != nil {
			return v.r.at(keyStart, err)
		}
		if keyKind == TextString && !v.p.allowTextKeys {
			return v.r.at(keyStart, v.p.refuse("text keys"))
		}
		if err := v.item(depth + 1); err != nil {
			return err
		}
		key := v.r.data[keyStart:v.r.off]
		if prev != nil && v.p.keyOrder.compare(prev, key) >= 0 {
			return v.r.at(keyStart, fmt.Errorf("%w: map keys are not in %s order", ErrMalformed, v.p.keyOrder))
		}
		prev = key
		if err := v.item(depth + 1); err != nil {
			return err
		}
	}
	return nil
}
