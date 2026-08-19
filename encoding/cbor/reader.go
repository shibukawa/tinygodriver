package cbor

import (
	"encoding/binary"
	"fmt"
	"math"
	"unicode/utf8"
)

// Reader decodes CBOR from a byte slice already in memory. It is the
// counterpart of Decoder for callers that have the whole item: it borrows
// strings from the input instead of copying them, skips an item without
// allocating it, and captures a sub-item at any depth, none of which an
// incremental io.Reader decoder can do.
//
// Every slice a Reader returns aliases the input. They stay valid for as long
// as the caller keeps the input alive and does not modify it; Reset severs
// nothing, so a slice from a previous input outlives the Reader but not the
// bytes behind it. Callers that need independent storage should copy.
//
// A Reader is not safe for concurrent use. Reuse one per connection rather than
// allocating one per message; that is what Reset is for.
//
// # Nesting
//
// Skip, ReadRaw and Profile.Validate drive their own recursion and bound it by
// DecoderOptions.MaxNestedLevels. ReadArrayHeader and ReadMapHeader do not:
// they read one head and return, and the walk over the container is the
// caller's own loop, so its depth is the caller's to bound. This differs from
// Decoder, which keeps a frame stack and refuses a container past the limit
// from ReadToken.
//
// The difference is deliberate. A frame stack is the state a Reader does not
// keep, and keeping one would cost an allocation per reader in the path that
// exists to have none. For untrusted input, call Profile.Validate first: it
// answers whether the bytes are legal under a bound without decoding them into
// anything, and generated code walking a fixed schema then recurses no deeper
// than the schema does.
type Reader struct {
	data []byte
	off  int
	opts DecoderOptions
}

// NewReader returns a Reader over data. A zero limit in opts selects the same
// conservative default NewDecoder uses.
func NewReader(data []byte, opts DecoderOptions) (*Reader, error) {
	if err := normalizeDecoderOptions(&opts); err != nil {
		return nil, err
	}
	return &Reader{data: data, opts: opts}, nil
}

// ReaderOver returns a Reader by value, for a caller that wants it on the stack.
//
// NewReader returns a pointer, which escapes, and a DecodeCBORFrom
// implementation runs once per field per message: at that rate the constructor
// is the allocation. A negative limit is treated as unset here rather than
// refused, because there is no error to return.
func ReaderOver(data []byte, opts DecoderOptions) Reader {
	if opts.MaxInputBytes < 0 {
		opts.MaxInputBytes = 0
	}
	if opts.MaxNestedLevels < 0 {
		opts.MaxNestedLevels = 0
	}
	if opts.MaxContainerItems < 0 || opts.MaxContainerItems > maxSliceLen/2 {
		opts.MaxContainerItems = 0
	}
	if opts.MaxStringBytes < 0 {
		opts.MaxStringBytes = 0
	}
	if opts.MaxRawMessageBytes < 0 {
		opts.MaxRawMessageBytes = 0
	}
	_ = normalizeDecoderOptions(&opts)
	return Reader{data: data, opts: opts}
}

// Reset points the Reader at a new input, keeping its options. Slices returned
// before the call still alias the old input.
func (r *Reader) Reset(data []byte) {
	r.data = data
	r.off = 0
}

// Offset reports how many bytes have been consumed, which is where the next
// item begins.
func (r *Reader) Offset() int { return r.off }

// Remaining reports how many bytes are left unconsumed.
func (r *Reader) Remaining() int { return len(r.data) - r.off }

// Done reports whether the whole input has been consumed.
func (r *Reader) Done() bool { return r.off >= len(r.data) }

func (r *Reader) take(n int) ([]byte, error) {
	if n < 0 || r.off+n > len(r.data) {
		return nil, ErrTruncated
	}
	b := r.data[r.off : r.off+n]
	r.off += n
	return b, nil
}

// head consumes one item head and returns its major type, additional
// information, and argument. indefinite reports ai == 31, whose meaning depends
// on the major type and is left to the caller.
func (r *Reader) head() (major, ai byte, arg uint64, indefinite bool, err error) {
	b, err := r.take(1)
	if err != nil {
		return 0, 0, 0, false, err
	}
	major, ai = b[0]>>5, b[0]&0x1f
	switch {
	case ai < 24:
		return major, ai, uint64(ai), false, nil
	case ai <= 27:
		v, err := r.take(1 << (ai - 24))
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

// Peek reports the kind of the next item without consuming it. It is the way to
// branch on an optional field: a caller that would otherwise have to capture
// and rewind can dispatch instead.
func (r *Reader) Peek() (TokenKind, error) {
	if r.off >= len(r.data) {
		return InvalidToken, ErrTruncated
	}
	b := r.data[r.off]
	major, ai := b>>5, b&0x1f
	if b == 0xff {
		return EndArray, nil
	}
	switch major {
	case 0:
		return UnsignedInteger, nil
	case 1:
		return NegativeInteger, nil
	case 2:
		return ByteString, nil
	case 3:
		return TextString, nil
	case 4:
		return StartArray, nil
	case 5:
		return StartMap, nil
	case 6:
		return Tag, nil
	default:
		switch ai {
		case 20, 21:
			return Boolean, nil
		case 22:
			return Null, nil
		case 25, 26, 27:
			return Float, nil
		}
		return InvalidToken, r.at(r.off, fmt.Errorf("%w: unsupported simple value %d", ErrMalformed, ai))
	}
}

// Skip advances past exactly one complete item, at any depth, without
// allocating and without materializing what it passed. It is how a decoder
// tolerates a field it does not know: the alternative, capturing the item only
// to discard it, is what makes an evolving schema expensive.
func (r *Reader) Skip() error {
	start := r.off
	if err := r.skipItem(0); err != nil {
		return r.at(start, err)
	}
	return nil
}

// skipItem positions whatever it refuses at the item that caused it, rather
// than at wherever the caller started skipping. Each level wraps its own start,
// and r.at leaves an error that already carries a position alone, so the
// innermost frame is the one that names the offset.
func (r *Reader) skipItem(depth int) error {
	start := r.off
	err := r.skipItemAt(depth)
	if err == nil {
		return nil
	}
	return r.at(start, err)
}

func (r *Reader) skipItemAt(depth int) error {
	if depth > r.opts.MaxNestedLevels {
		return fmt.Errorf("%w: nesting depth", ErrLimitExceeded)
	}
	major, ai, arg, indefinite, err := r.head()
	if err != nil {
		return err
	}
	switch major {
	case 0, 1:
		if indefinite {
			return fmt.Errorf("%w: invalid indefinite item", ErrMalformed)
		}
		return nil
	case 2, 3:
		return r.skipString(major, arg, indefinite)
	case 4:
		return r.skipContainer(depth, arg, indefinite, 1)
	case 5:
		return r.skipContainer(depth, arg, indefinite, 2)
	case 6:
		if indefinite {
			return fmt.Errorf("%w: invalid indefinite item", ErrMalformed)
		}
		return r.skipItem(depth + 1)
	default:
		switch ai {
		case 20, 21, 22:
			return nil
		case 25, 26, 27:
			if r.opts.RejectFloats {
				return ErrFloatRefused
			}
			return nil
		default:
			return fmt.Errorf("%w: unsupported simple value %d", ErrMalformed, ai)
		}
	}
}

func (r *Reader) skipString(major byte, arg uint64, indefinite bool) error {
	if !indefinite {
		if arg > uint64(r.opts.MaxStringBytes) || arg > uint64(maxSliceLen) {
			return fmt.Errorf("%w: string bytes", ErrLimitExceeded)
		}
		_, err := r.take(int(arg))
		return err
	}
	total := uint64(0)
	for {
		b, err := r.take(1)
		if err != nil {
			return err
		}
		if b[0] == 0xff {
			return nil
		}
		r.off--
		chunkMajor, _, chunkArg, chunkIndefinite, err := r.head()
		if err != nil {
			return err
		}
		if chunkIndefinite || chunkMajor != major {
			return fmt.Errorf("%w: invalid indefinite string chunk", ErrMalformed)
		}
		total += chunkArg
		if total > uint64(r.opts.MaxStringBytes) || chunkArg > uint64(maxSliceLen) {
			return fmt.Errorf("%w: string bytes", ErrLimitExceeded)
		}
		if _, err := r.take(int(chunkArg)); err != nil {
			return err
		}
	}
}

// skipContainer walks arg items, or perItem items per entry for a map.
func (r *Reader) skipContainer(depth int, arg uint64, indefinite bool, perItem uint64) error {
	if !indefinite {
		if arg > uint64(r.opts.MaxContainerItems) {
			return fmt.Errorf("%w: container items", ErrLimitExceeded)
		}
		for i := uint64(0); i < arg*perItem; i++ {
			if err := r.skipItem(depth + 1); err != nil {
				return err
			}
		}
		return nil
	}
	items := 0
	for {
		b, err := r.take(1)
		if err != nil {
			return err
		}
		if b[0] == 0xff {
			return nil
		}
		r.off--
		if items >= r.opts.MaxContainerItems {
			return fmt.Errorf("%w: container items", ErrLimitExceeded)
		}
		for i := uint64(0); i < perItem; i++ {
			if err := r.skipItem(depth + 1); err != nil {
				return err
			}
		}
		items++
	}
}

// ReadRaw returns the bytes of exactly one item, at whatever depth the Reader
// currently sits, and advances past it. The result aliases the input.
//
// This is the primitive a type that carries its own decoding needs: a field of
// a foreign type is always nested inside something, and handing that type its
// own bytes is the only way to decode it without knowing its shape.
//
// The captured item is measured from zero against MaxNestedLevels, so the
// depth already spent reaching it does not count against it. Decoding an
// envelope field by field therefore costs the larger of the envelope's depth
// and its payload's, where validating the whole document at once costs their
// sum. See Profile.Validate.
func (r *Reader) ReadRaw() (RawMessage, error) {
	start := r.off
	if err := r.skipItem(0); err != nil {
		return nil, r.at(start, err)
	}
	raw := r.data[start:r.off]
	if len(raw) > r.opts.MaxRawMessageBytes {
		return nil, r.at(start, fmt.Errorf("%w: raw message bytes", ErrLimitExceeded))
	}
	return RawMessage(raw), nil
}

// expect consumes one head and requires the major type given. A refusal rewinds,
// so the caller can dispatch on something else instead.
func (r *Reader) expect(want byte, kind TokenKind) (uint64, bool, error) {
	start := r.off
	major, _, arg, indefinite, err := r.head()
	if err != nil {
		r.off = start
		return 0, false, r.at(start, err)
	}
	if major != want {
		r.off = start
		return 0, false, r.at(start, fmt.Errorf("%w: got major type %d, want %s", ErrUnexpectedToken, major, kind))
	}
	return arg, indefinite, nil
}

// ReadUint reads an unsigned integer.
func (r *Reader) ReadUint() (uint64, error) {
	arg, indefinite, err := r.expect(0, UnsignedInteger)
	if err != nil {
		return 0, err
	}
	if indefinite {
		return 0, r.at(r.off-1, fmt.Errorf("%w: invalid indefinite item", ErrMalformed))
	}
	return arg, nil
}

// ReadInt reads an integer of either sign. A negative integer outside the int64
// range is ErrIntegerOverflow; ReadRaw preserves it where the value itself is
// needed.
func (r *Reader) ReadInt() (int64, error) {
	start := r.off
	major, _, arg, indefinite, err := r.head()
	if err != nil {
		r.off = start
		return 0, r.at(start, err)
	}
	if indefinite || (major != 0 && major != 1) {
		r.off = start
		return 0, r.at(start, fmt.Errorf("%w: got major type %d, want integer", ErrUnexpectedToken, major))
	}
	if arg > math.MaxInt64 {
		return 0, r.at(start, ErrIntegerOverflow)
	}
	if major == 1 {
		return -1 - int64(arg), nil
	}
	return int64(arg), nil
}

// Sized integer reads. The encoder writes the shortest form, so a field
// declared int32 arrives as anything from one to five bytes and its width is
// carried by the schema rather than the bytes. These enforce the declared
// width, so a value outside it is a protocol error rather than a silent wrap.
func (r *Reader) ReadInt8() (int8, error) {
	v, err := r.readSigned(8)
	return int8(v), err
}

func (r *Reader) ReadInt16() (int16, error) {
	v, err := r.readSigned(16)
	return int16(v), err
}

func (r *Reader) ReadInt32() (int32, error) {
	v, err := r.readSigned(32)
	return int32(v), err
}

// ReadInt64 is ReadInt under the name the sized set uses.
func (r *Reader) ReadInt64() (int64, error) { return r.ReadInt() }

func (r *Reader) ReadUint8() (uint8, error) {
	v, err := r.readUnsigned(8)
	return uint8(v), err
}

func (r *Reader) ReadUint16() (uint16, error) {
	v, err := r.readUnsigned(16)
	return uint16(v), err
}

func (r *Reader) ReadUint32() (uint32, error) {
	v, err := r.readUnsigned(32)
	return uint32(v), err
}

// ReadUint64 is ReadUint under the name the sized set uses.
func (r *Reader) ReadUint64() (uint64, error) { return r.ReadUint() }

func (r *Reader) readSigned(bits uint) (int64, error) {
	start := r.off
	v, err := r.ReadInt()
	if err != nil {
		return 0, err
	}
	limit := int64(1) << (bits - 1)
	if v < -limit || v > limit-1 {
		return 0, r.at(start, fmt.Errorf("%w: %d does not fit int%d", ErrIntegerOverflow, v, bits))
	}
	return v, nil
}

func (r *Reader) readUnsigned(bits uint) (uint64, error) {
	start := r.off
	v, err := r.ReadUint()
	if err != nil {
		return 0, err
	}
	if v > (uint64(1)<<bits)-1 {
		return 0, r.at(start, fmt.Errorf("%w: %d does not fit uint%d", ErrIntegerOverflow, v, bits))
	}
	return v, nil
}

// ReadBytes returns a byte string borrowed from the input.
func (r *Reader) ReadBytes() ([]byte, error) { return r.readString(2, ByteString) }

// ReadTextBytes returns a text string borrowed from the input, without the
// allocation ReadText's string conversion costs.
func (r *Reader) ReadTextBytes() ([]byte, error) {
	b, err := r.readString(3, TextString)
	if err != nil {
		return nil, err
	}
	if !utf8.Valid(b) {
		return nil, r.at(r.off-len(b), fmt.Errorf("%w: invalid UTF-8", ErrMalformed))
	}
	return b, nil
}

// ReadText returns a text string as a Go string, which copies it.
func (r *Reader) ReadText() (string, error) {
	b, err := r.ReadTextBytes()
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (r *Reader) readString(major byte, kind TokenKind) ([]byte, error) {
	start := r.off
	arg, indefinite, err := r.expect(major, kind)
	if err != nil {
		return nil, err
	}
	if indefinite {
		// The chunks are not contiguous, so nothing can be borrowed. Callers
		// wanting indefinite input should Skip or ReadRaw it.
		r.off = start
		return nil, r.at(start, fmt.Errorf("%w: indefinite string cannot be borrowed", ErrUnexpectedToken))
	}
	if arg > uint64(r.opts.MaxStringBytes) || arg > uint64(maxSliceLen) {
		r.off = start
		return nil, r.at(start, fmt.Errorf("%w: string bytes", ErrLimitExceeded))
	}
	b, err := r.take(int(arg))
	if err != nil {
		r.off = start
		return nil, r.at(start, err)
	}
	return b, nil
}

// ReadBool reads a boolean.
func (r *Reader) ReadBool() (bool, error) {
	start := r.off
	major, ai, _, _, err := r.head()
	if err != nil {
		r.off = start
		return false, r.at(start, err)
	}
	if major != 7 || (ai != 20 && ai != 21) {
		r.off = start
		return false, r.at(start, fmt.Errorf("%w: want boolean", ErrUnexpectedToken))
	}
	return ai == 21, nil
}

// ReadNull reads the null value.
func (r *Reader) ReadNull() error {
	start := r.off
	major, ai, _, _, err := r.head()
	if err != nil {
		r.off = start
		return r.at(start, err)
	}
	if major != 7 || ai != 22 {
		r.off = start
		return r.at(start, fmt.Errorf("%w: want null", ErrUnexpectedToken))
	}
	return nil
}

// ReadTag reads a tag head and leaves its content as the next item.
func (r *Reader) ReadTag() (uint64, error) {
	arg, indefinite, err := r.expect(6, Tag)
	if err != nil {
		return 0, err
	}
	if indefinite {
		return 0, r.at(r.off-1, fmt.Errorf("%w: invalid indefinite item", ErrMalformed))
	}
	return arg, nil
}

// ReadFloat reads a half, single, or double precision float.
func (r *Reader) ReadFloat() (float64, error) {
	start := r.off
	major, ai, _, _, err := r.head()
	if err != nil {
		r.off = start
		return 0, r.at(start, err)
	}
	if major != 7 {
		r.off = start
		return 0, r.at(start, fmt.Errorf("%w: want float", ErrUnexpectedToken))
	}
	if r.opts.RejectFloats {
		r.off = start
		return 0, r.at(start, ErrFloatRefused)
	}
	switch ai {
	case 25:
		return float16(binary.BigEndian.Uint16(r.data[r.off-2 : r.off])), nil
	case 26:
		return float64(math.Float32frombits(binary.BigEndian.Uint32(r.data[r.off-4 : r.off]))), nil
	case 27:
		return math.Float64frombits(binary.BigEndian.Uint64(r.data[r.off-8 : r.off])), nil
	default:
		r.off = start
		return 0, r.at(start, fmt.Errorf("%w: want float", ErrUnexpectedToken))
	}
}

// ReadArrayHeader reads an array head and returns its length. A definite-length
// array reports indefinite false and its item count; an indefinite one reports
// true and a length of -1, and ends at the item Peek reports as EndArray.
//
// It bounds the item count but not the nesting depth, which the caller's own
// walk owns. See the Nesting section of the Reader documentation.
func (r *Reader) ReadArrayHeader() (length int, indefinite bool, err error) {
	return r.readContainerHeader(4, StartArray, 1)
}

// ReadMapHeader reads a map head and returns its pair count, on the same terms
// as ReadArrayHeader, nesting included.
func (r *Reader) ReadMapHeader() (pairs int, indefinite bool, err error) {
	return r.readContainerHeader(5, StartMap, 2)
}

func (r *Reader) readContainerHeader(major byte, kind TokenKind, perItem uint64) (int, bool, error) {
	start := r.off
	arg, indefinite, err := r.expect(major, kind)
	if err != nil {
		return 0, false, err
	}
	if indefinite {
		return -1, true, nil
	}
	if arg > uint64(r.opts.MaxContainerItems) {
		r.off = start
		return 0, false, r.at(start, fmt.Errorf("%w: container items", ErrLimitExceeded))
	}
	if arg*perItem > uint64(maxSliceLen) {
		r.off = start
		return 0, false, r.at(start, fmt.Errorf("%w: container items", ErrLimitExceeded))
	}
	return int(arg), false, nil
}

// ReadBreak consumes the break that ends an indefinite-length container.
func (r *Reader) ReadBreak() error {
	if r.off >= len(r.data) {
		return r.at(r.off, ErrTruncated)
	}
	if r.data[r.off] != 0xff {
		return r.at(r.off, fmt.Errorf("%w: want break", ErrUnexpectedToken))
	}
	r.off++
	return nil
}
