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

type containerFrame struct {
	kind       TokenKind
	remaining  int64
	indefinite bool
	items      int
	maxItems   int
}

// Decoder incrementally reads CBOR from an io.Reader without io.ReadAll.
// ReadToken exposes container boundaries; typed Read methods are conveniences
// that reject a token of the wrong kind.
type Decoder struct {
	r           io.Reader
	opts        DecoderOptions
	read        int64
	stack       []containerFrame
	rootOpen    bool
	rootDone    bool
	finished    bool
	pendingTags int
	capture     *[]byte
	tokenSource *Decoder
	// scratch backs the fixed-width argument reads. It lives on the Decoder
	// because a local array handed to io.ReadFull escapes, which would put an
	// allocation on every head byte that carries an argument.
	scratch [8]byte
	// one backs readByte for the same reason scratch backs argument: handed to
	// an io.Reader through the interface, a local array escapes, and readByte
	// runs once per head. It is separate from scratch so neither read can
	// clobber the other's bytes.
	one [1]byte
	// keyBuf backs duplicate map key detection. One buffer serves every depth,
	// because a key is canonicalized and recorded before the scan descends into
	// its value.
	keyBuf []byte
}

func NewDecoder(r io.Reader, opts DecoderOptions) (*Decoder, error) {
	if r == nil {
		return nil, errorsf("nil reader")
	}
	if err := normalizeDecoderOptions(&opts); err != nil {
		return nil, err
	}
	return &Decoder{r: r, opts: opts}, nil
}

func normalizeDecoderOptions(o *DecoderOptions) error {
	if *o == (DecoderOptions{}) {
		*o = defaultDecoderOptions
		return nil
	}
	if o.MaxInputBytes < 0 || o.MaxNestedLevels < 0 || o.MaxContainerItems < 0 || o.MaxStringBytes < 0 || o.MaxRawMessageBytes < 0 {
		return fmt.Errorf("cbor: limits must not be negative")
	}
	if o.MaxInputBytes == 0 {
		o.MaxInputBytes = defaultMaxInputBytes
	}
	if o.MaxNestedLevels == 0 {
		o.MaxNestedLevels = defaultMaxNestedLevels
	}
	if o.MaxContainerItems == 0 {
		o.MaxContainerItems = defaultMaxContainerItems
	}
	// Half of maxSliceLen, because a map frame counts keys and values
	// separately and so doubles this bound. Capping it here is what lets every
	// later container length be converted to an int without a second check:
	// an item count that got past this fits, on whichever width is compiling.
	if o.MaxContainerItems > maxSliceLen/2 {
		return fmt.Errorf("cbor: MaxContainerItems is too large")
	}
	if o.MaxStringBytes == 0 {
		o.MaxStringBytes = defaultMaxStringBytes
	}
	if o.MaxRawMessageBytes == 0 {
		o.MaxRawMessageBytes = defaultMaxRawMessageBytes
	}
	return nil
}

func errorsf(detail string) error { return fmt.Errorf("cbor: %s", detail) }

func (d *Decoder) readByte() (byte, error) {
	b := &d.one
	n, err := io.ReadFull(d.r, b[:])
	if n == 1 {
		if d.read >= d.opts.MaxInputBytes {
			return 0, fmt.Errorf("%w: input bytes", ErrLimitExceeded)
		}
		d.read++
		if d.capture != nil {
			if len(*d.capture) >= d.opts.MaxRawMessageBytes {
				return 0, fmt.Errorf("%w: raw message bytes", ErrLimitExceeded)
			}
			*d.capture = append(*d.capture, b[0])
		}
		return b[0], nil
	}
	if err == io.EOF {
		return 0, io.EOF
	}
	return 0, ErrTruncated
}

// readFull reads exactly len(p) bytes, accounting them against the input budget
// and the raw capture the way readByte does. Bytes that did arrive are still
// accounted when the read comes up short, so the reader is not left describing
// a position it has already passed.
func (d *Decoder) readFull(p []byte) error {
	if len(p) == 0 {
		return nil
	}
	if remaining := d.opts.MaxInputBytes - d.read; remaining < 0 || int64(len(p)) > remaining {
		return fmt.Errorf("%w: input bytes", ErrLimitExceeded)
	}
	n, err := io.ReadFull(d.r, p)
	if n > 0 {
		d.read += int64(n)
		if d.capture != nil {
			if len(*d.capture)+n > d.opts.MaxRawMessageBytes {
				return fmt.Errorf("%w: raw message bytes", ErrLimitExceeded)
			}
			*d.capture = append(*d.capture, p[:n]...)
		}
	}
	if err != nil {
		return ErrTruncated
	}
	return nil
}

// readChunkBytes caps the buffer readBytes reserves before any payload arrives.
// Past it the slice grows as bytes actually turn up, so the allocation follows
// the input rather than the number the input claimed.
const readChunkBytes = 4096

func (d *Decoder) readBytes(n uint64, stringLimit bool) ([]byte, error) {
	if n > uint64(maxSliceLen) {
		return nil, fmt.Errorf("%w: item length", ErrLimitExceeded)
	}
	if stringLimit && n > uint64(d.opts.MaxStringBytes) {
		return nil, fmt.Errorf("%w: string bytes", ErrLimitExceeded)
	}
	// A length larger than the input budget still allows cannot be satisfied, so
	// refusing here reaches the same answer readByte would — before anything is
	// reserved rather than after, and naming the same limit it would have named.
	if remaining := d.opts.MaxInputBytes - d.read; remaining < 0 || n > uint64(remaining) {
		return nil, fmt.Errorf("%w: input bytes", ErrLimitExceeded)
	}
	// The length prefix is attacker-controlled and arrives before its payload:
	// five bytes declaring a megabyte used to reserve a megabyte and then report
	// a truncated item. This reaches an unauthenticated caller through a passkey
	// attestation, so the amplification was a request the size of a header
	// costing the whole configured string bound in live heap.
	//
	// The buffer therefore grows a chunk at a time, and each chunk is filled
	// from the reader before the next one is reserved. A declared length still
	// never becomes an allocation on its own, but a string that does arrive now
	// costs one read per chunk instead of one read per byte.
	total := int(n)
	b := make([]byte, 0, min(total, readChunkBytes))
	for len(b) < total {
		base := len(b)
		want := min(total-base, readChunkBytes)
		b = slices.Grow(b, want)[:base+want]
		if err := d.readFull(b[base:]); err != nil {
			return nil, err
		}
	}
	return b, nil
}

func (d *Decoder) argument(ai byte) (uint64, error) {
	switch {
	case ai < 24:
		return uint64(ai), nil
	case ai <= 27:
		width := 1 << (ai - 24) // 1, 2, 4 or 8
		p := d.scratch[:width]
		if err := d.readFull(p); err != nil {
			return 0, truncated(err)
		}
		switch width {
		case 1:
			return uint64(p[0]), nil
		case 2:
			return uint64(binary.BigEndian.Uint16(p)), nil
		case 4:
			return uint64(binary.BigEndian.Uint32(p)), nil
		default:
			return binary.BigEndian.Uint64(p), nil
		}
	default:
		return 0, fmt.Errorf("%w: reserved additional information %d", ErrMalformed, ai)
	}
}

func truncated(err error) error {
	if err == io.EOF {
		return ErrTruncated
	}
	return err
}

func (d *Decoder) ensureValueSlot() error {
	if len(d.stack) == 0 {
		return nil
	}
	f := &d.stack[len(d.stack)-1]
	if !f.indefinite && f.remaining == 0 {
		return fmt.Errorf("%w: container already complete", ErrMalformed)
	}
	if f.items >= f.maxItems {
		return fmt.Errorf("%w: container items", ErrLimitExceeded)
	}
	return nil
}

func (d *Decoder) beginValue(container bool, frame containerFrame) error {
	if err := d.ensureValueSlot(); err != nil {
		return err
	}
	if len(d.stack) > 0 {
		p := &d.stack[len(d.stack)-1]
		p.items++
		if !p.indefinite {
			p.remaining--
		}
	} else {
		if d.rootDone && !d.opts.Sequence {
			return ErrExtraneousData
		}
		d.rootOpen = true
	}
	d.pendingTags = 0
	if container {
		if len(d.stack)+1 > d.opts.MaxNestedLevels {
			return fmt.Errorf("%w: nesting depth", ErrLimitExceeded)
		}
		d.stack = append(d.stack, frame)
	} else if len(d.stack) == 0 {
		d.rootOpen = false
		d.rootDone = true
	}
	return nil
}

func (d *Decoder) endContainer() Token {
	f := d.stack[len(d.stack)-1]
	d.stack = d.stack[:len(d.stack)-1]
	if len(d.stack) == 0 {
		d.rootOpen = false
		d.rootDone = true
	}
	return Token{Kind: map[TokenKind]TokenKind{StartArray: EndArray, StartMap: EndMap}[f.kind]}
}

func (d *Decoder) prepareRoot() error {
	if d.finished {
		return io.EOF
	}
	if !d.rootDone {
		return nil
	}
	if d.opts.Sequence {
		d.rootDone = false
		return nil
	}
	b, err := d.readByte()
	if err == io.EOF {
		d.finished = true
		return io.EOF
	}
	if err != nil {
		return err
	}
	_ = b
	d.finished = true
	return ErrExtraneousData
}

// ReadToken reads the next typed token. Definite container end tokens are
// synthesized when the declared item count is exhausted. For a non-sequence
// decoder, the call after the root token returns io.EOF only after confirming
// that no trailing byte exists.
func (d *Decoder) ReadToken() (Token, error) {
	if d.opts.RejectDuplicateMapKeys {
		for {
			if d.tokenSource != nil {
				tok, err := d.tokenSource.ReadToken()
				if err != io.EOF {
					return tok, err
				}
				d.tokenSource = nil
				if !d.opts.Sequence {
					return Token{}, io.EOF
				}
			}
			raw, err := d.ReadRaw()
			if err != nil {
				return Token{}, err
			}
			opts := d.opts
			opts.RejectDuplicateMapKeys = false
			opts.Sequence = false
			opts.MaxInputBytes = int64(len(raw))
			d.tokenSource, err = NewDecoder(bytes.NewReader(raw), opts)
			if err != nil {
				return Token{}, err
			}
		}
	}
	if err := d.prepareRoot(); err != nil {
		return Token{}, err
	}
	if len(d.stack) > 0 {
		f := &d.stack[len(d.stack)-1]
		if !f.indefinite && f.remaining == 0 {
			if d.pendingTags != 0 {
				return Token{}, fmt.Errorf("%w: tag without content", ErrMalformed)
			}
			return d.endContainer(), nil
		}
	}
	b, err := d.readByte()
	if err != nil {
		if err == io.EOF && (d.rootOpen || len(d.stack) > 0 || d.pendingTags > 0) {
			return Token{}, ErrTruncated
		}
		return Token{}, err
	}
	major, ai := b>>5, b&0x1f
	if b == 0xff {
		if len(d.stack) == 0 || !d.stack[len(d.stack)-1].indefinite {
			return Token{}, fmt.Errorf("%w: unexpected break", ErrMalformed)
		}
		if d.pendingTags != 0 {
			return Token{}, fmt.Errorf("%w: tag without content", ErrMalformed)
		}
		f := &d.stack[len(d.stack)-1]
		if f.kind == StartMap && f.items%2 != 0 {
			return Token{}, fmt.Errorf("%w: map break after key", ErrMalformed)
		}
		return d.endContainer(), nil
	}
	if ai == 31 && (major < 2 || major > 5) {
		return Token{}, fmt.Errorf("%w: invalid indefinite item", ErrMalformed)
	}

	switch major {
	case 0, 1:
		arg, err := d.argument(ai)
		if err != nil {
			return Token{}, err
		}
		kind := UnsignedInteger
		if major == 1 {
			kind = NegativeInteger
		}
		if err := d.beginValue(false, containerFrame{}); err != nil {
			return Token{}, err
		}
		return Token{Kind: kind, Argument: arg}, nil
	case 2, 3:
		data, indefinite, err := d.readString(major, ai)
		if err != nil {
			return Token{}, err
		}
		if major == 3 && !utf8.Valid(data) {
			return Token{}, fmt.Errorf("%w: invalid UTF-8", ErrMalformed)
		}
		if err := d.beginValue(false, containerFrame{}); err != nil {
			return Token{}, err
		}
		if major == 2 {
			return Token{Kind: ByteString, Bytes: data, Length: len(data), Indefinite: indefinite}, nil
		}
		return Token{Kind: TextString, Text: string(data), Length: len(data), Indefinite: indefinite}, nil
	case 4, 5:
		kind := StartArray
		if major == 5 {
			kind = StartMap
		}
		frame := containerFrame{kind: kind, maxItems: d.opts.MaxContainerItems}
		if major == 5 {
			frame.maxItems *= 2
		}
		tok := Token{Kind: kind}
		if ai == 31 {
			frame.indefinite, frame.remaining = true, -1
			tok.Indefinite, tok.Length = true, -1
		} else {
			n, err := d.argument(ai)
			if err != nil {
				return Token{}, err
			}
			if n > uint64(d.opts.MaxContainerItems) {
				return Token{}, fmt.Errorf("%w: container items", ErrLimitExceeded)
			}
			// n is already at or below MaxContainerItems, which
			// normalizeDecoderOptions capped at maxSliceLen/2. Both the
			// doubling below and the conversion to int are therefore in range
			// on a 64-bit server and on a 32-bit or js/wasm client alike, and
			// neither needs a guard of its own.
			remaining := n
			if major == 5 {
				remaining *= 2
			}
			frame.remaining = int64(remaining)
			tok.Length = int(n)
		}
		if err := d.beginValue(true, frame); err != nil {
			return Token{}, err
		}
		return tok, nil
	case 6:
		arg, err := d.argument(ai)
		if err != nil {
			return Token{}, err
		}
		if err := d.ensureValueSlot(); err != nil {
			return Token{}, err
		}
		if len(d.stack) == 0 && d.rootDone && !d.opts.Sequence {
			return Token{}, ErrExtraneousData
		}
		if len(d.stack) == 0 {
			d.rootOpen = true
		}
		if len(d.stack)+d.pendingTags+1 > d.opts.MaxNestedLevels {
			return Token{}, fmt.Errorf("%w: nesting depth", ErrLimitExceeded)
		}
		d.pendingTags++
		return Token{Kind: Tag, Argument: arg}, nil
	case 7:
		return d.readSimple(ai)
	default:
		panic("unreachable")
	}
}

func (d *Decoder) readString(major, ai byte) ([]byte, bool, error) {
	if ai != 31 {
		n, err := d.argument(ai)
		if err != nil {
			return nil, false, err
		}
		b, err := d.readBytes(n, true)
		return b, false, err
	}
	var out []byte
	for {
		h, err := d.readByte()
		if err != nil {
			return nil, true, truncated(err)
		}
		if h == 0xff {
			return out, true, nil
		}
		if h>>5 != major || h&0x1f == 31 {
			return nil, true, fmt.Errorf("%w: invalid indefinite string chunk", ErrMalformed)
		}
		n, err := d.argument(h & 0x1f)
		if err != nil {
			return nil, true, err
		}
		if n > uint64(d.opts.MaxStringBytes-len(out)) {
			return nil, true, fmt.Errorf("%w: string bytes", ErrLimitExceeded)
		}
		chunk, err := d.readBytes(n, true)
		if err != nil {
			return nil, true, err
		}
		out = append(out, chunk...)
	}
}

func (d *Decoder) readSimple(ai byte) (Token, error) {
	var tok Token
	switch ai {
	case 20:
		tok = Token{Kind: Boolean, Bool: false}
	case 21:
		tok = Token{Kind: Boolean, Bool: true}
	case 22:
		tok = Token{Kind: Null}
	case 25:
		if d.opts.RejectFloats {
			return Token{}, ErrFloatRefused
		}
		b, err := d.readBytes(2, false)
		if err != nil {
			return Token{}, err
		}
		tok = Token{Kind: Float, Float: float16(binary.BigEndian.Uint16(b))}
	case 26:
		if d.opts.RejectFloats {
			return Token{}, ErrFloatRefused
		}
		b, err := d.readBytes(4, false)
		if err != nil {
			return Token{}, err
		}
		tok = Token{Kind: Float, Float: float64(math.Float32frombits(binary.BigEndian.Uint32(b)))}
	case 27:
		if d.opts.RejectFloats {
			return Token{}, ErrFloatRefused
		}
		b, err := d.readBytes(8, false)
		if err != nil {
			return Token{}, err
		}
		tok = Token{Kind: Float, Float: math.Float64frombits(binary.BigEndian.Uint64(b))}
	default:
		return Token{}, fmt.Errorf("%w: unsupported simple value %d", ErrMalformed, ai)
	}
	if err := d.beginValue(false, containerFrame{}); err != nil {
		return Token{}, err
	}
	return tok, nil
}

func float16(h uint16) float64 {
	sign := uint64(h>>15) << 63
	exp, frac := (h>>10)&0x1f, h&0x3ff
	if exp == 0 {
		if frac == 0 {
			return math.Float64frombits(sign)
		}
		v := math.Ldexp(float64(frac), -24)
		if sign != 0 {
			v = -v
		}
		return v
	}
	if exp == 31 {
		if frac == 0 {
			return math.Float64frombits(sign | 0x7ff0000000000000)
		}
		return math.NaN()
	}
	bits := sign | uint64(exp-15+1023)<<52 | uint64(frac)<<42
	return math.Float64frombits(bits)
}

func (d *Decoder) expect(kind TokenKind) (Token, error) {
	t, err := d.ReadToken()
	if err != nil {
		return Token{}, err
	}
	if t.Kind != kind {
		return Token{}, fmt.Errorf("%w: got %s, want %s", ErrUnexpectedToken, t.Kind, kind)
	}
	return t, nil
}

func (d *Decoder) ReadUint() (uint64, error) { t, e := d.expect(UnsignedInteger); return t.Argument, e }
func (d *Decoder) ReadInt() (int64, error) {
	t, e := d.ReadToken()
	if e != nil {
		return 0, e
	}
	return t.Int64()
}
func (d *Decoder) ReadBytes() ([]byte, error)  { t, e := d.expect(ByteString); return t.Bytes, e }
func (d *Decoder) ReadText() (string, error)   { t, e := d.expect(TextString); return t.Text, e }
func (d *Decoder) ReadBool() (bool, error)     { t, e := d.expect(Boolean); return t.Bool, e }
func (d *Decoder) ReadNull() error             { _, e := d.expect(Null); return e }
func (d *Decoder) ReadTag() (uint64, error)    { t, e := d.expect(Tag); return t.Argument, e }
func (d *Decoder) ReadFloat() (float64, error) { t, e := d.expect(Float); return t.Float, e }
func (d *Decoder) ReadArray() (length int, indefinite bool, err error) {
	t, e := d.expect(StartArray)
	return t.Length, t.Indefinite, e
}
func (d *Decoder) ReadMap() (pairs int, indefinite bool, err error) {
	t, e := d.expect(StartMap)
	return t.Length, t.Indefinite, e
}

// ReadRaw reads and validates exactly one item while retaining at most
// MaxRawMessageBytes. In non-sequence mode it also rejects trailing data.
func (d *Decoder) ReadRaw() (RawMessage, error) {
	if d.finished {
		return nil, io.EOF
	}
	if d.tokenSource != nil {
		return nil, errorsf("ReadRaw cannot follow a partial token stream")
	}
	if d.rootOpen || len(d.stack) != 0 || d.pendingTags != 0 {
		return nil, errorsf("ReadRaw cannot follow a partial token stream")
	}
	if d.rootDone {
		if !d.opts.Sequence {
			return nil, io.EOF
		}
		d.rootDone = false
	}
	raw := make([]byte, 0, 128)
	d.capture = &raw
	b, err := d.readByte()
	if err == nil {
		err = d.scanItemHead(0, b)
	}
	d.capture = nil
	if err == io.EOF {
		return nil, io.EOF
	}
	if err != nil {
		return nil, err
	}
	d.rootDone = true
	if !d.opts.Sequence {
		_, err := d.readByte()
		if err == nil {
			d.finished = true
			return nil, ErrExtraneousData
		}
		if err != io.EOF {
			return nil, err
		}
		d.finished = true
	}
	return RawMessage(raw), nil
}

func (d *Decoder) scanItem(depth int) error {
	b, err := d.readByte()
	if err != nil {
		return truncated(err)
	}
	return d.scanItemHead(depth, b)
}

func (d *Decoder) scanItemHead(depth int, b byte) error {
	major, ai := b>>5, b&0x1f
	if ai >= 28 && ai <= 30 {
		return fmt.Errorf("%w: reserved additional information %d", ErrMalformed, ai)
	}
	if b == 0xff {
		return fmt.Errorf("%w: unexpected break", ErrMalformed)
	}
	switch major {
	case 0, 1, 6:
		if ai == 31 {
			return fmt.Errorf("%w: invalid indefinite item", ErrMalformed)
		}
		if _, err := d.argument(ai); err != nil {
			return err
		}
		if major == 6 {
			if depth+1 > d.opts.MaxNestedLevels {
				return fmt.Errorf("%w: nesting depth", ErrLimitExceeded)
			}
			return d.scanItem(depth + 1)
		}
		return nil
	case 2, 3:
		data, _, err := d.readString(major, ai)
		if err != nil {
			return err
		}
		if major == 3 && !utf8.Valid(data) {
			return fmt.Errorf("%w: invalid UTF-8", ErrMalformed)
		}
		return nil
	case 4:
		if depth+1 > d.opts.MaxNestedLevels {
			return fmt.Errorf("%w: nesting depth", ErrLimitExceeded)
		}
		return d.scanContainer(depth+1, ai, false)
	case 5:
		if depth+1 > d.opts.MaxNestedLevels {
			return fmt.Errorf("%w: nesting depth", ErrLimitExceeded)
		}
		return d.scanContainer(depth+1, ai, true)
	case 7:
		switch ai {
		case 20, 21, 22:
			return nil
		case 25, 26, 27:
			if d.opts.RejectFloats {
				return ErrFloatRefused
			}
			_, err := d.readBytes(1<<(ai-24), false)
			return err
		default:
			return fmt.Errorf("%w: unsupported simple value %d", ErrMalformed, ai)
		}
	default:
		return fmt.Errorf("%w: invalid major type", ErrMalformed)
	}
}

func (d *Decoder) scanContainer(depth int, ai byte, isMap bool) error {
	indef := ai == 31
	var count uint64
	if !indef {
		n, err := d.argument(ai)
		if err != nil {
			return err
		}
		count = n
		if count > uint64(d.opts.MaxContainerItems) {
			return fmt.Errorf("%w: container items", ErrLimitExceeded)
		}
	}
	var seen map[string]struct{}
	items := 0
	for {
		if !indef && uint64(items) >= count {
			break
		}
		if indef {
			b, err := d.readByte()
			if err != nil {
				return truncated(err)
			}
			if b == 0xff {
				break
			}
			if items >= d.opts.MaxContainerItems {
				return fmt.Errorf("%w: container items", ErrLimitExceeded)
			}
			if isMap {
				start := len(*d.capture) - 1
				if err := d.scanItemHead(depth, b); err != nil {
					return err
				}
				key := (*d.capture)[start:]
				if d.opts.RejectDuplicateMapKeys {
					if err := d.noteMapKey(&seen, key); err != nil {
						return err
					}
				}
				if err := d.scanItem(depth); err != nil {
					return err
				}
				items++
				continue
			}
			if err := d.scanItemHead(depth, b); err != nil {
				return err
			}
			items++
			continue
		}
		if isMap {
			start := len(*d.capture)
			if err := d.scanItem(depth); err != nil {
				return err
			}
			key := (*d.capture)[start:]
			if d.opts.RejectDuplicateMapKeys {
				if err := d.noteMapKey(&seen, key); err != nil {
					return err
				}
			}
			if indef {
				// A break is legal only where the next key would begin, never as a value.
			}
			if err := d.scanItem(depth); err != nil {
				return err
			}
		} else {
			if err := d.scanItem(depth); err != nil {
				return err
			}
		}
		items++
	}
	return nil
}

// noteMapKey canonicalizes one encoded map key and records it, reporting a
// duplicate against every key already recorded for the same map. The set is
// created on first use, so a map without detection enabled, and every array,
// costs nothing.
func (d *Decoder) noteMapKey(seen *map[string]struct{}, key []byte) error {
	buf, err := canonicalizeKey(d.keyBuf[:0], key, d.opts.MaxNestedLevels)
	if err != nil {
		return err
	}
	d.keyBuf = buf
	if *seen == nil {
		*seen = make(map[string]struct{})
	}
	if _, ok := (*seen)[string(buf)]; ok {
		return ErrDuplicateMapKey
	}
	(*seen)[string(buf)] = struct{}{}
	return nil
}

// Validate checks that data contains exactly one bounded CBOR item.
func Validate(data []byte, opts DecoderOptions) error {
	opts.Sequence = false
	d, err := NewDecoder(bytes.NewReader(data), opts)
	if err != nil {
		return err
	}
	_, err = d.ReadRaw()
	return err
}
