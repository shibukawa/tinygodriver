package cbor

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"slices"
)

// Duplicate map key detection cannot compare encoded keys directly. CBOR lets
// the same value be written more than one way -- a non-minimal argument, an
// indefinite-length string split into different chunks, a map whose members
// arrive in a different order -- and two keys that denote the same value are
// duplicates however they were spelled.
//
// canonicalKey rewrites one item into a form where equal values are equal byte
// strings, so the detector can be a set over those bytes. Every argument is
// widened to a fixed eight bytes, so 0x05 and 0x18 0x05 converge; string chunks
// are concatenated; floats collapse to float64 bits with one NaN; and map
// members are sorted. The result is never decoded again, so it only has to be
// injective, not legible.
//
// The canonical form of a k-byte item is under 9k+9 bytes, and k is already
// bounded by MaxRawMessageBytes, so the buffer this fills is bounded too.

const (
	canonUint   = 'u'
	canonNeg    = 'n'
	canonBytes  = 'b'
	canonText   = 's'
	canonArray  = 'a'
	canonMap    = 'm'
	canonTag    = 't'
	canonFalse  = 'F'
	canonTrue   = 'T'
	canonNull   = 'N'
	canonFloat  = 'f'
	canonBreak  = 0xff
	canonWidth8 = 8
)

func appendArg(dst []byte, tag byte, arg uint64) []byte {
	dst = append(dst, tag)
	return binary.BigEndian.AppendUint64(dst, arg)
}

// canonKey is a cursor over one already-scanned CBOR item.
type canonKey struct {
	raw   []byte
	off   int
	depth int
	max   int
}

func (c *canonKey) take(n int) ([]byte, error) {
	if n < 0 || c.off+n > len(c.raw) {
		return nil, ErrTruncated
	}
	b := c.raw[c.off : c.off+n]
	c.off += n
	return b, nil
}

func (c *canonKey) head() (major, ai byte, arg uint64, indefinite bool, err error) {
	b, err := c.take(1)
	if err != nil {
		return 0, 0, 0, false, err
	}
	major, ai = b[0]>>5, b[0]&0x1f
	switch {
	case ai < 24:
		return major, ai, uint64(ai), false, nil
	case ai <= 27:
		v, err := c.take(1 << (ai - 24))
		if err != nil {
			return 0, 0, 0, false, err
		}
		switch len(v) {
		case 1:
			arg = uint64(v[0])
		case 2:
			arg = uint64(binary.BigEndian.Uint16(v))
		case 4:
			arg = uint64(binary.BigEndian.Uint32(v))
		default:
			arg = binary.BigEndian.Uint64(v)
		}
		return major, ai, arg, false, nil
	case ai == 31:
		return major, ai, 0, true, nil
	default:
		return 0, 0, 0, false, fmt.Errorf("%w: reserved additional information %d", ErrMalformed, ai)
	}
}

// item appends the canonical form of the next item to dst.
func (c *canonKey) item(dst []byte) ([]byte, error) {
	if c.depth > c.max {
		return nil, fmt.Errorf("%w: nesting depth", ErrLimitExceeded)
	}
	major, ai, arg, indefinite, err := c.head()
	if err != nil {
		return nil, err
	}
	switch major {
	case 0:
		return appendArg(dst, canonUint, arg), nil
	case 1:
		return appendArg(dst, canonNeg, arg), nil
	case 2, 3:
		tag := byte(canonBytes)
		if major == 3 {
			tag = canonText
		}
		return c.stringItem(dst, tag, arg, indefinite)
	case 4:
		return c.arrayItem(dst, arg, indefinite)
	case 5:
		return c.mapItem(dst, arg, indefinite)
	case 6:
		dst = appendArg(dst, canonTag, arg)
		c.depth++
		dst, err = c.item(dst)
		c.depth--
		return dst, err
	case 7:
		return c.simpleItem(dst, ai)
	}
	return nil, fmt.Errorf("%w: invalid major type", ErrMalformed)
}

func (c *canonKey) stringItem(dst []byte, tag byte, arg uint64, indefinite bool) ([]byte, error) {
	if !indefinite {
		payload, err := c.take(int(arg))
		if err != nil {
			return nil, err
		}
		dst = appendArg(dst, tag, arg)
		return append(dst, payload...), nil
	}
	// An indefinite string is the concatenation of its chunks, so the length is
	// only known once they have all been seen. Reserve the field and patch it.
	dst = appendArg(dst, tag, 0)
	lengthAt := len(dst) - canonWidth8
	total := uint64(0)
	for {
		b, err := c.take(1)
		if err != nil {
			return nil, err
		}
		if b[0] == canonBreak {
			break
		}
		c.off--
		chunkMajor, _, chunkArg, chunkIndefinite, err := c.head()
		if err != nil {
			return nil, err
		}
		if chunkIndefinite || (chunkMajor != 2 && chunkMajor != 3) {
			return nil, fmt.Errorf("%w: invalid indefinite string chunk", ErrMalformed)
		}
		payload, err := c.take(int(chunkArg))
		if err != nil {
			return nil, err
		}
		dst = append(dst, payload...)
		total += chunkArg
	}
	binary.BigEndian.PutUint64(dst[lengthAt:], total)
	return dst, nil
}

func (c *canonKey) arrayItem(dst []byte, arg uint64, indefinite bool) ([]byte, error) {
	dst = appendArg(dst, canonArray, arg)
	countAt := len(dst) - canonWidth8
	c.depth++
	defer func() { c.depth-- }()
	if !indefinite {
		for i := uint64(0); i < arg; i++ {
			var err error
			if dst, err = c.item(dst); err != nil {
				return nil, err
			}
		}
		return dst, nil
	}
	count := uint64(0)
	for {
		b, err := c.take(1)
		if err != nil {
			return nil, err
		}
		if b[0] == canonBreak {
			break
		}
		c.off--
		if dst, err = c.item(dst); err != nil {
			return nil, err
		}
		count++
	}
	binary.BigEndian.PutUint64(dst[countAt:], count)
	return dst, nil
}

// mapItem sorts its members, because a map used as a map key denotes the same
// value whatever order its members were written in. This is the one branch that
// allocates, and it is reachable only from a map nested inside a key, which no
// profile this package serves permits.
func (c *canonKey) mapItem(dst []byte, arg uint64, indefinite bool) ([]byte, error) {
	c.depth++
	defer func() { c.depth-- }()
	var pairs [][]byte
	appendPair := func() error {
		pair, err := c.item(nil)
		if err != nil {
			return err
		}
		if pair, err = c.item(pair); err != nil {
			return err
		}
		pairs = append(pairs, pair)
		return nil
	}
	if !indefinite {
		for i := uint64(0); i < arg; i++ {
			if err := appendPair(); err != nil {
				return nil, err
			}
		}
	} else {
		for {
			b, err := c.take(1)
			if err != nil {
				return nil, err
			}
			if b[0] == canonBreak {
				break
			}
			c.off--
			if err := appendPair(); err != nil {
				return nil, err
			}
		}
	}
	slices.SortFunc(pairs, bytes.Compare)
	dst = appendArg(dst, canonMap, uint64(len(pairs)))
	for _, pair := range pairs {
		dst = append(dst, pair...)
	}
	return dst, nil
}

func (c *canonKey) simpleItem(dst []byte, ai byte) ([]byte, error) {
	switch ai {
	case 20:
		return append(dst, canonFalse), nil
	case 21:
		return append(dst, canonTrue), nil
	case 22:
		return append(dst, canonNull), nil
	case 25, 26, 27:
		// The argument was already consumed by head, so re-read it from the
		// bytes behind the cursor rather than tracking it through the switch.
		width := 1 << (ai - 24)
		bits := c.raw[c.off-width : c.off]
		var v float64
		switch width {
		case 2:
			v = float16(binary.BigEndian.Uint16(bits))
		case 4:
			v = float64(math.Float32frombits(binary.BigEndian.Uint32(bits)))
		default:
			v = math.Float64frombits(binary.BigEndian.Uint64(bits))
		}
		raw := math.Float64bits(v)
		if math.IsNaN(v) {
			raw = 0x7ff8000000000000
		}
		return appendArg(dst, canonFloat, raw), nil
	default:
		return nil, fmt.Errorf("%w: %d cannot begin a map key", ErrMalformed, ai)
	}
}

// canonicalizeKey appends the canonical form of the single item in raw to dst.
func canonicalizeKey(dst, raw []byte, maxDepth int) ([]byte, error) {
	c := canonKey{raw: raw, max: maxDepth}
	dst, err := c.item(dst)
	if err != nil {
		return nil, err
	}
	if c.off != len(raw) {
		return nil, ErrExtraneousData
	}
	return dst, nil
}
