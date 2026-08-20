package cbor

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"slices"
	"unicode/utf8"
)

// Encoder writes deterministic RFC 8949. Each method writes one complete item.
// WriteArray and WriteMap validate their child RawMessages before writing;
// WriteMap sorts keys under EncoderOptions.KeyOrder, which defaults to the
// length-first ordering CTAP2 and COSE require rather than to RFC 8949 Core
// Deterministic Encoding. Indefinite-length output is not supported, because it
// cannot be deterministic.
type Encoder struct {
	w    io.Writer
	opts EncoderOptions
	// buf backs every write. Building each item into a buffer the Encoder owns
	// keeps the per-item allocation out of the steady state: the buffer grows to
	// the largest item this encoder has written and stays there. io.Writer
	// forbids retaining the slice, so handing it out again on the next call is
	// legal.
	buf []byte
}

func NewEncoder(w io.Writer, opts EncoderOptions) (*Encoder, error) {
	if w == nil {
		return nil, errorsf("nil writer")
	}
	if opts.MaxNestedLevels < 0 || opts.MaxContainerItems < 0 || opts.MaxStringBytes < 0 {
		return nil, errorsf("limits must not be negative")
	}
	if opts.MaxNestedLevels == 0 {
		opts.MaxNestedLevels = defaultMaxNestedLevels
	}
	if opts.MaxContainerItems == 0 {
		opts.MaxContainerItems = defaultMaxContainerItems
	}
	if opts.MaxStringBytes == 0 {
		opts.MaxStringBytes = defaultMaxStringBytes
	}
	return &Encoder{w: w, opts: opts}, nil
}

func (e *Encoder) write(p []byte) error {
	for len(p) > 0 {
		n, err := e.w.Write(p)
		if n > 0 {
			p = p[n:]
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

// flush writes the item just built in e.buf and keeps the grown buffer for the
// next one.
func (e *Encoder) flush(b []byte) error {
	e.buf = b[:0]
	return e.write(b)
}

func appendHead(dst []byte, major byte, n uint64) []byte {
	switch {
	case n < 24:
		return append(dst, major<<5|byte(n))
	case n <= math.MaxUint8:
		return append(dst, major<<5|24, byte(n))
	case n <= math.MaxUint16:
		dst = append(dst, major<<5|25, 0, 0)
		binary.BigEndian.PutUint16(dst[len(dst)-2:], uint16(n))
		return dst
	case n <= math.MaxUint32:
		dst = append(dst, major<<5|26, 0, 0, 0, 0)
		binary.BigEndian.PutUint32(dst[len(dst)-4:], uint32(n))
		return dst
	default:
		dst = append(dst, major<<5|27, 0, 0, 0, 0, 0, 0, 0, 0)
		binary.BigEndian.PutUint64(dst[len(dst)-8:], n)
		return dst
	}
}

func (e *Encoder) WriteUint(v uint64) error { return e.flush(appendHead(e.buf[:0], 0, v)) }
func (e *Encoder) WriteInt(v int64) error {
	if v >= 0 {
		return e.WriteUint(uint64(v))
	}
	return e.flush(appendHead(e.buf[:0], 1, uint64(-(v + 1))))
}
func (e *Encoder) WriteBytes(v []byte) error {
	if len(v) > e.opts.MaxStringBytes {
		return fmt.Errorf("%w: byte string", ErrLimitExceeded)
	}
	b := appendHead(e.buf[:0], 2, uint64(len(v)))
	b = append(b, v...)
	return e.flush(b)
}
func (e *Encoder) WriteText(v string) error {
	if !utf8.ValidString(v) {
		return fmt.Errorf("%w: invalid UTF-8", ErrMalformed)
	}
	if len(v) > e.opts.MaxStringBytes {
		return fmt.Errorf("%w: text string", ErrLimitExceeded)
	}
	b := appendHead(e.buf[:0], 3, uint64(len(v)))
	b = append(b, v...)
	return e.flush(b)
}
func (e *Encoder) WriteBool(v bool) error {
	if v {
		return e.flush(append(e.buf[:0], 0xf5))
	}
	return e.flush(append(e.buf[:0], 0xf4))
}
func (e *Encoder) WriteNull() error           { return e.flush(append(e.buf[:0], 0xf6)) }
func (e *Encoder) WriteFloat(v float64) error { return e.flush(appendFloat(e.buf[:0], v)) }

func (e *Encoder) WriteTag(tag uint64, content RawMessage) error {
	if err := e.validateRaw(content); err != nil {
		return err
	}
	b := appendHead(e.buf[:0], 6, tag)
	b = append(b, content...)
	return e.flush(b)
}

// WriteRaw writes one already deterministic item after validating it.
func (e *Encoder) WriteRaw(raw RawMessage) error {
	if err := e.validateRaw(raw); err != nil {
		return err
	}
	return e.write(raw)
}

func (e *Encoder) validateRaw(raw RawMessage) error {
	if len(raw) == 0 {
		return ErrTruncated
	}
	p := deterministicParser{data: raw, opts: e.opts}
	if err := p.item(0); err != nil {
		return err
	}
	if p.off != len(raw) {
		return ErrExtraneousData
	}
	return nil
}

func (e *Encoder) WriteArray(items []RawMessage) error {
	if len(items) > e.opts.MaxContainerItems {
		return fmt.Errorf("%w: array items", ErrLimitExceeded)
	}
	b := appendHead(e.buf[:0], 4, uint64(len(items)))
	for _, item := range items {
		if err := e.validateRaw(item); err != nil {
			return err
		}
		b = append(b, item...)
	}
	return e.flush(b)
}

func (e *Encoder) WriteMap(entries []MapEntry) error {
	if len(entries) > e.opts.MaxContainerItems {
		return fmt.Errorf("%w: map pairs", ErrLimitExceeded)
	}
	ordered := append([]MapEntry(nil), entries...)
	for _, entry := range ordered {
		if err := e.validateRaw(entry.Key); err != nil {
			return fmt.Errorf("cbor: map key: %w", err)
		}
		if err := e.validateRaw(entry.Value); err != nil {
			return fmt.Errorf("cbor: map value: %w", err)
		}
	}
	order := e.opts.KeyOrder
	slices.SortFunc(ordered, func(a, b MapEntry) int { return order.compare(a.Key, b.Key) })
	for i := 1; i < len(ordered); i++ {
		if bytes.Equal(ordered[i-1].Key, ordered[i].Key) {
			return ErrDuplicateMapKey
		}
	}
	b := appendHead(e.buf[:0], 5, uint64(len(ordered)))
	for _, entry := range ordered {
		b = append(b, entry.Key...)
		b = append(b, entry.Value...)
	}
	return e.flush(b)
}

func marshalFloat(v float64) []byte { return appendFloat(nil, v) }

// appendFloat appends a float in the shortest form that round-trips, into the
// caller's buffer, so the streaming and append paths share one encoding.
func appendFloat(dst []byte, v float64) []byte {
	if math.IsNaN(v) {
		return append(dst, 0xf9, 0x7e, 0x00)
	}
	if h, ok := exactHalf(v); ok {
		return append(dst, 0xf9, byte(h>>8), byte(h))
	}
	f := float32(v)
	if float64(f) == v {
		b := math.Float32bits(f)
		return append(dst, 0xfa, byte(b>>24), byte(b>>16), byte(b>>8), byte(b))
	}
	b := math.Float64bits(v)
	return append(dst, 0xfb, byte(b>>56), byte(b>>48), byte(b>>40), byte(b>>32), byte(b>>24), byte(b>>16), byte(b>>8), byte(b))
}

func exactHalf(v float64) (uint16, bool) {
	if math.IsNaN(v) {
		return 0x7e00, true
	}
	f := float32(v)
	if float64(f) != v {
		return 0, false
	}
	b := math.Float32bits(f)
	sign := uint16(b>>31) << 15
	exp := int((b >> 23) & 0xff)
	mant := b & 0x7fffff
	if exp == 255 {
		if mant == 0 {
			return sign | 0x7c00, true
		}
		return sign | 0x7e00, true
	}
	if exp == 0 {
		if mant == 0 {
			return sign, true
		}
		return 0, false
	}
	e := exp - 127
	var h uint16
	if e >= -14 && e <= 15 {
		if mant&0x1fff != 0 {
			return 0, false
		}
		h = sign | uint16(e+15)<<10 | uint16(mant>>13)
	} else if e >= -24 && e < -14 {
		full := mant | 0x800000
		shift := uint(-e - 1)
		mask := uint32(1<<shift) - 1
		if full&mask != 0 {
			return 0, false
		}
		h = sign | uint16(full>>shift)
	} else {
		return 0, false
	}
	return h, float16(h) == v
}

// Raw constructors make deterministic primitive values convenient to use in
// WriteArray and WriteMap.
func MarshalUint(v uint64) RawMessage { return RawMessage(appendHead(nil, 0, v)) }
func MarshalInt(v int64) RawMessage {
	if v >= 0 {
		return MarshalUint(uint64(v))
	}
	return RawMessage(appendHead(nil, 1, uint64(-(v + 1))))
}
func MarshalBytes(v []byte) RawMessage {
	b := appendHead(nil, 2, uint64(len(v)))
	return RawMessage(append(b, v...))
}
func MarshalText(v string) (RawMessage, error) {
	if !utf8.ValidString(v) {
		return nil, fmt.Errorf("%w: invalid UTF-8", ErrMalformed)
	}
	b := appendHead(nil, 3, uint64(len(v)))
	return RawMessage(append(b, v...)), nil
}
func MarshalBool(v bool) RawMessage {
	if v {
		return RawMessage{0xf5}
	}
	return RawMessage{0xf4}
}
func MarshalNull() RawMessage           { return RawMessage{0xf6} }
func MarshalFloat(v float64) RawMessage { return RawMessage(marshalFloat(v)) }

type deterministicParser struct {
	data []byte
	off  int
	opts EncoderOptions
}

func (p *deterministicParser) take(n int) ([]byte, error) {
	if n < 0 || p.off+n > len(p.data) {
		return nil, ErrTruncated
	}
	b := p.data[p.off : p.off+n]
	p.off += n
	return b, nil
}

func (p *deterministicParser) head() (byte, byte, uint64, error) {
	b, err := p.take(1)
	if err != nil {
		return 0, 0, 0, err
	}
	major, ai := b[0]>>5, b[0]&0x1f
	if ai < 24 {
		return major, ai, uint64(ai), nil
	}
	var n int
	switch ai {
	case 24:
		n = 1
	case 25:
		n = 2
	case 26:
		n = 4
	case 27:
		n = 8
	default:
		return 0, 0, 0, fmt.Errorf("%w: indefinite or reserved encoding", ErrMalformed)
	}
	v, err := p.take(n)
	if err != nil {
		return 0, 0, 0, err
	}
	var arg uint64
	switch n {
	case 1:
		arg = uint64(v[0])
	case 2:
		arg = uint64(binary.BigEndian.Uint16(v))
	case 4:
		arg = uint64(binary.BigEndian.Uint32(v))
	case 8:
		arg = binary.BigEndian.Uint64(v)
	}
	if major != 7 && ((n == 1 && arg < 24) || (n == 2 && arg <= math.MaxUint8) || (n == 4 && arg <= math.MaxUint16) || (n == 8 && arg <= math.MaxUint32)) {
		return 0, 0, 0, fmt.Errorf("%w: non-minimal argument", ErrMalformed)
	}
	return major, ai, arg, nil
}

func (p *deterministicParser) item(depth int) error {
	start := p.off
	major, ai, arg, err := p.head()
	if err != nil {
		return err
	}
	switch major {
	case 0, 1:
		return nil
	case 2, 3:
		if arg > uint64(p.opts.MaxStringBytes) || arg > uint64(math.MaxInt) {
			return fmt.Errorf("%w: string bytes", ErrLimitExceeded)
		}
		b, err := p.take(int(arg))
		if err != nil {
			return err
		}
		if major == 3 && !utf8.Valid(b) {
			return fmt.Errorf("%w: invalid UTF-8", ErrMalformed)
		}
		return nil
	case 4:
		if depth+1 > p.opts.MaxNestedLevels {
			return fmt.Errorf("%w: nesting depth", ErrLimitExceeded)
		}
		if arg > uint64(p.opts.MaxContainerItems) {
			return fmt.Errorf("%w: array items", ErrLimitExceeded)
		}
		for i := uint64(0); i < arg; i++ {
			if err := p.item(depth + 1); err != nil {
				return err
			}
		}
		return nil
	case 5:
		if depth+1 > p.opts.MaxNestedLevels {
			return fmt.Errorf("%w: nesting depth", ErrLimitExceeded)
		}
		if arg > uint64(p.opts.MaxContainerItems) {
			return fmt.Errorf("%w: map pairs", ErrLimitExceeded)
		}
		var prev []byte
		for i := uint64(0); i < arg; i++ {
			ks := p.off
			if err := p.item(depth + 1); err != nil {
				return err
			}
			key := p.data[ks:p.off]
			if prev != nil && p.opts.KeyOrder.compare(prev, key) >= 0 {
				return fmt.Errorf("%w: map keys are not in %s order", ErrMalformed, p.opts.KeyOrder)
			}
			prev = key
			if err := p.item(depth + 1); err != nil {
				return err
			}
		}
		return nil
	case 6:
		if depth+1 > p.opts.MaxNestedLevels {
			return fmt.Errorf("%w: nesting depth", ErrLimitExceeded)
		}
		return p.item(depth + 1)
	case 7:
		switch ai {
		case 20, 21, 22:
			return nil
		case 25:
			bits := binary.BigEndian.Uint16(p.data[p.off-2 : p.off])
			if math.IsNaN(float16(bits)) && bits != 0x7e00 {
				return fmt.Errorf("%w: non-canonical NaN", ErrMalformed)
			}
			return nil
		case 26:
			v := float64(math.Float32frombits(binary.BigEndian.Uint32(p.data[p.off-4 : p.off])))
			if math.IsNaN(v) {
				return fmt.Errorf("%w: non-canonical NaN", ErrMalformed)
			}
			if _, ok := exactHalf(v); ok {
				return fmt.Errorf("%w: float is not shortest", ErrMalformed)
			}
			return nil
		case 27:
			v := math.Float64frombits(binary.BigEndian.Uint64(p.data[p.off-8 : p.off]))
			if math.IsNaN(v) {
				return fmt.Errorf("%w: non-canonical NaN", ErrMalformed)
			}
			if float64(float32(v)) == v {
				return fmt.Errorf("%w: float is not shortest", ErrMalformed)
			}
			return nil
		default:
			return fmt.Errorf("%w: unsupported simple value", ErrMalformed)
		}
	default:
		return fmt.Errorf("%w at byte %d", ErrMalformed, start)
	}
}
