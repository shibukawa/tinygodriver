package cbor

import (
	"fmt"
)

// A Profile is a named restriction on which CBOR is legal: a subset of the
// format that both ends of a protocol agree on. CTAP2 canonical CBOR, COSE and
// RFC 8949 section 4.2 deterministic encoding are all restrictions of this kind.
//
// A Profile carries no resource limits, and the distinction is the point.
//
// What a profile says is a property of the protocol: both peers must agree on
// it, a disagreement is a defect, and changing it changes the format. How large
// an item a particular process is willing to read is a property of that
// process: a dedicated server and a browser client can hold different answers,
// and there is nothing to agree on. Those live in DecoderOptions, chosen per
// deployment, and are passed alongside a profile rather than baked into one.
//
// Mixing them, which this type used to do, makes a deployment decision look
// like a protocol change and hides a protocol change inside a deployment one.
//
// The zero value restricts nothing: every well-formed CBOR item is legal under
// it. Build one by naming what to refuse.
//
//	wire := cbor.Profile{
//		Name:             "wire",
//		RejectMaps:       true,
//		RejectTags:       true,
//		RejectFloats:     true,
//		RejectIndefinite: true,
//		RejectTextKeys:   true,
//	}
//
// A Profile only enforces. It never infers which profile a message should be
// read under; that belongs to the schema and to the caller.
type Profile struct {
	// Name appears in this profile's refusals. It is otherwise unused.
	Name string

	// RequireSortedKeys demands that map keys appear in KeyOrder, strictly
	// ascending. RFC 8949 deterministic encoding requires this; plain CBOR does
	// not, so the zero value does not either.
	RequireSortedKeys bool
	// KeyOrder selects which deterministic ordering RequireSortedKeys demands.
	// See KeyOrder: the two RFC 8949 orderings produce different bytes.
	KeyOrder KeyOrder

	// RejectMaps refuses major type 5 outright. A format that encodes structs
	// as fixed-order arrays has no use for it, and refusing it is what keeps
	// field names off the wire.
	RejectMaps bool
	// RejectTags refuses major type 6.
	RejectTags bool
	// RejectFloats refuses every float, at encode and at decode. A format
	// carrying scaled integers wants this: a float in it is a protocol
	// violation rather than a value.
	RejectFloats bool
	// RejectIndefinite refuses indefinite-length items. Deterministic encoding
	// requires this, since an indefinite item has more than one spelling.
	RejectIndefinite bool
	// RejectTextKeys refuses a text string as a map key, leaving integer
	// labels. COSE and CTAP2 use integer labels for the same reason.
	RejectTextKeys bool
}

// Canonical returns the profile CTAP2 canonical CBOR and COSE require: map keys
// in length-first order, no indefinite lengths, everything else permitted.
//
// This is the shape a WebAuthn attestation is checked against. Floats are not
// refused, because nothing in COSE forbids them.
func Canonical() Profile {
	return Profile{
		Name:              "canonical",
		RequireSortedKeys: true,
		KeyOrder:          LengthFirstKeyOrder,
		RejectIndefinite:  true,
	}
}

// Deterministic returns RFC 8949 section 4.2.1 Core Deterministic Encoding:
// the same restrictions as Canonical with bytewise key ordering instead of
// length-first.
func Deterministic() Profile {
	return Profile{
		Name:              "deterministic",
		RequireSortedKeys: true,
		KeyOrder:          BytewiseKeyOrder,
		RejectIndefinite:  true,
	}
}

// applyTo returns opts with the decoder settings this profile implies. Only
// float refusal crosses over; every other restriction is checked by Validate,
// and every limit in opts is the caller's.
func (p Profile) applyTo(opts DecoderOptions) DecoderOptions {
	if p.RejectFloats {
		opts.RejectFloats = true
	}
	return opts
}

// NewReader returns a Reader over data with the caller's limits and this
// profile's decoder-visible restrictions.
func (p Profile) NewReader(data []byte, opts DecoderOptions) (*Reader, error) {
	return NewReader(data, p.applyTo(opts))
}

// ReaderOver returns a Reader by value on the same terms as NewReader. See
// ReaderOver.
func (p Profile) ReaderOver(data []byte, opts DecoderOptions) Reader {
	return ReaderOver(data, p.applyTo(opts))
}

// Validate reports whether data is exactly one item legal under p and within
// the limits in opts. It answers the question without decoding the item into
// anything, which is what makes it usable as a boundary check on bytes from
// somewhere else.
//
// # Depth arithmetic
//
// Validate walks the whole document, so nesting adds up: an envelope that wraps
// a message costs the envelope's depth plus the message's. A patch or delta
// carrying a subtree of a document is therefore deeper than the document.
//
// Reading does not add up the same way. Reader.ReadRaw measures a captured item
// from zero, so decoding an envelope field by field and handing each payload on
// as raw bytes costs the larger of the two depths rather than their sum.
//
// The default nesting bound is a stack safety net set far past any schema, so
// neither arithmetic matters unless a caller narrows it deliberately.
func (p Profile) Validate(data []byte, opts DecoderOptions) error {
	opts = p.applyTo(opts)
	if err := normalizeDecoderOptions(&opts); err != nil {
		return err
	}
	v := profileValidator{p: p, r: Reader{data: data, opts: opts}}
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
func (p Profile) ValidateAppended(dst []byte, before int, opts DecoderOptions) error {
	if before < 0 || before > len(dst) {
		return errorsf("ValidateAppended: length before the call is out of range")
	}
	appended := dst[before:]
	if len(appended) == 0 {
		return &Error{Offset: int64(before), Err: fmt.Errorf("%w: appended nothing, want one item", ErrTruncated)}
	}
	if err := p.Validate(appended, opts); err != nil {
		if e, ok := err.(*Error); ok {
			e.Offset += int64(before)
		}
		return err
	}
	return nil
}

func (p Profile) refuse(what string) error {
	name := p.Name
	if name == "" {
		name = "this"
	}
	return fmt.Errorf("%w: the %s profile does not permit %s", ErrProfileViolation, name, what)
}

// profileValidator walks one item, checking it against a profile as it goes.
type profileValidator struct {
	p Profile
	r Reader
}

func (v *profileValidator) item(depth int) error {
	start := v.r.off
	if depth > v.r.opts.MaxNestedLevels {
		return v.r.at(start, fmt.Errorf("%w: nesting depth", ErrLimitExceeded))
	}
	major, ai, arg, indefinite, err := v.r.head()
	if err != nil {
		return v.r.at(start, err)
	}
	// An indefinite length is only meaningful on a string, an array or a map.
	// On an integer or a tag the head is malformed whatever the profile says,
	// so that check comes first: a profile permitting indefinite lengths is
	// permitting the legal ones, not waiving well-formedness.
	if indefinite && major != 2 && major != 3 && major != 4 && major != 5 && major != 7 {
		return v.r.at(start, fmt.Errorf("%w: invalid indefinite item", ErrMalformed))
	}
	if indefinite && v.p.RejectIndefinite {
		return v.r.at(start, v.p.refuse("indefinite lengths"))
	}
	switch major {
	case 0, 1:
		return nil
	case 2, 3:
		return v.stringItem(start, major, arg, indefinite)
	case 4:
		return v.arrayItem(depth, start, arg, indefinite)
	case 5:
		if v.p.RejectMaps {
			return v.r.at(start, v.p.refuse("maps"))
		}
		return v.mapItem(depth, start, arg, indefinite)
	case 6:
		if v.p.RejectTags {
			return v.r.at(start, v.p.refuse("tags"))
		}
		return v.item(depth + 1)
	default:
		switch ai {
		case 20, 21, 22:
			return nil
		case 25, 26, 27:
			if v.p.RejectFloats {
				return v.r.at(start, ErrFloatRefused)
			}
			return nil
		default:
			return v.r.at(start, fmt.Errorf("%w: unsupported simple value %d", ErrMalformed, ai))
		}
	}
}

func (v *profileValidator) stringItem(start int, major byte, arg uint64, indefinite bool) error {
	if !indefinite {
		if arg > uint64(v.r.opts.MaxStringBytes) || arg > uint64(maxSliceLen) {
			return v.r.at(start, fmt.Errorf("%w: string bytes", ErrLimitExceeded))
		}
		if _, err := v.r.take(int(arg)); err != nil {
			return v.r.at(start, err)
		}
		return nil
	}
	return v.r.at(start, v.r.skipString(major, 0, true))
}

func (v *profileValidator) arrayItem(depth, start int, arg uint64, indefinite bool) error {
	if indefinite {
		return v.indefiniteContainer(depth, 1)
	}
	if arg > uint64(v.r.opts.MaxContainerItems) {
		return v.r.at(start, fmt.Errorf("%w: container items", ErrLimitExceeded))
	}
	for i := uint64(0); i < arg; i++ {
		if err := v.item(depth + 1); err != nil {
			return err
		}
	}
	return nil
}

func (v *profileValidator) mapItem(depth, start int, pairs uint64, indefinite bool) error {
	if indefinite {
		return v.indefiniteContainer(depth, 2)
	}
	if pairs > uint64(v.r.opts.MaxContainerItems) {
		return v.r.at(start, fmt.Errorf("%w: map pairs", ErrLimitExceeded))
	}
	var prev []byte
	for i := uint64(0); i < pairs; i++ {
		keyStart := v.r.off
		if err := v.key(depth); err != nil {
			return err
		}
		key := v.r.data[keyStart:v.r.off]
		if v.p.RequireSortedKeys && prev != nil && v.p.KeyOrder.compare(prev, key) >= 0 {
			return v.r.at(keyStart, fmt.Errorf("%w: map keys are not in %s order", ErrMalformed, v.p.KeyOrder))
		}
		prev = key
		if err := v.item(depth + 1); err != nil {
			return err
		}
	}
	return nil
}

func (v *profileValidator) key(depth int) error {
	keyStart := v.r.off
	kind, err := v.r.Peek()
	if err != nil {
		return v.r.at(keyStart, err)
	}
	if kind == TextString && v.p.RejectTextKeys {
		return v.r.at(keyStart, v.p.refuse("text keys"))
	}
	return v.item(depth + 1)
}

// indefiniteContainer walks a container that only a profile permitting
// indefinite lengths can reach.
func (v *profileValidator) indefiniteContainer(depth int, perItem int) error {
	items := 0
	for {
		if v.r.off >= len(v.r.data) {
			return v.r.at(v.r.off, ErrTruncated)
		}
		if v.r.data[v.r.off] == 0xff {
			v.r.off++
			return nil
		}
		if items >= v.r.opts.MaxContainerItems {
			return v.r.at(v.r.off, fmt.Errorf("%w: container items", ErrLimitExceeded))
		}
		for range perItem {
			if err := v.item(depth + 1); err != nil {
				return err
			}
		}
		items++
	}
}
