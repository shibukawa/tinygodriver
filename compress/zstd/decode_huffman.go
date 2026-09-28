//go:build tinygo || force_tinygo_logic

// The literals section, decoded: its header, the Huffman tree description in
// either weight representation, and one or four coded streams.

package zstd

import (
	"encoding/binary"
	"math/bits"
)

// Literals_Block_Type 3 reuses the previous block's Huffman table. The encoder
// never writes it; the reference encoder does whenever the old table is still
// good enough.
const literalsTreeless = 3

// maxHuffWeightLog is the accuracy limit for FSE-compressed Huffman weights.
const maxHuffWeightLog = 6

// huffDecEntry is one slot of a decoding table, indexed by the next maxBits
// bits of the stream: the symbol whose code starts with those bits, and how
// many of them the code actually takes.
type huffDecEntry struct {
	symbol uint8
	nbBits uint8
}

type huffDecTable struct {
	entries [1 << maxHuffBits]huffDecEntry
	maxBits uint8
}

// decodeLiterals decodes the literals section at the front of block, returning
// the literals and how many bytes the section took. Raw literals are returned
// in place, as a subslice of block.
func (d *decoder) decodeLiterals(block []byte) ([]byte, int, error) {
	if len(block) == 0 {
		return nil, 0, errCorruptLiterals
	}
	typ := block[0] & 3
	sizeFormat := block[0] >> 2 & 3

	if typ == literalsRaw || typ == literalsRLE {
		// One, two or three header bytes, the size in whatever bits remain
		// after the type and format fields; format 2 is format 0 again, its
		// second bit belonging to the size.
		var regen, h int
		switch sizeFormat {
		case 0, 2:
			h, regen = 1, int(block[0]>>3)
		case 1:
			if len(block) < 2 {
				return nil, 0, errCorruptLiterals
			}
			h, regen = 2, int(block[0])>>4|int(block[1])<<4
		case 3:
			if len(block) < 3 {
				return nil, 0, errCorruptLiterals
			}
			h, regen = 3, int(block[0])>>4|int(block[1])<<4|int(block[2])<<12
		}
		if regen > d.blockMax {
			return nil, 0, errCorruptLiterals
		}
		if typ == literalsRaw {
			if len(block) < h+regen {
				return nil, 0, errCorruptLiterals
			}
			return block[h : h+regen], h + regen, nil
		}
		if len(block) < h+1 {
			return nil, 0, errCorruptLiterals
		}
		lits := d.literalBuffer(regen)
		for i := range lits {
			lits[i] = block[h]
		}
		return lits, h + 1, nil
	}

	// Compressed and treeless literals state both sizes, in widths that grow
	// with the header; only format 0 is a single stream. The header is
	// assembled as a uint64, since at five bytes it outgrows a 32-bit int.
	h := [4]int{3, 3, 4, 5}[sizeFormat]
	if len(block) < h {
		return nil, 0, errCorruptLiterals
	}
	var v uint64
	for i := h - 1; i >= 0; i-- {
		v = v<<8 | uint64(block[i])
	}
	sizeBits := [4]uint{10, 10, 14, 18}[sizeFormat]
	regen := int(v >> 4 & (1<<sizeBits - 1))
	comp := int(v >> (4 + sizeBits))
	single := sizeFormat == 0
	if regen > d.blockMax || (!single && regen < 6) || len(block) < h+comp {
		// Four streams need six literals, as the reference decoder requires;
		// below that, some sizes would leave the last stream a negative share.
		return nil, 0, errCorruptLiterals
	}
	body := block[h : h+comp]

	if typ == literalsCompressed {
		n, err := d.readHuffTable(body)
		if err != nil {
			return nil, 0, err
		}
		d.huffValid = true
		body = body[n:]
	} else if !d.huffValid {
		return nil, 0, errCorruptLiterals
	}

	lits := d.literalBuffer(regen)
	var err error
	if single {
		err = d.huff.decodeStream(lits, body)
	} else {
		err = d.huff.decode4(lits, body)
	}
	return lits, h + comp, err
}

// literalBuffer returns n bytes of the decoder's literal storage, which is
// sized for the largest block this frame allows the first time it is needed.
func (d *decoder) literalBuffer(n int) []byte {
	if cap(d.lits) < n {
		d.lits = make([]byte, d.blockMax)
	}
	return d.lits[:n]
}

// readHuffTable reads a Huffman tree description from the front of src into
// d.huff and returns the bytes it took.
//
// A header byte of 128 or more introduces the direct representation, a nibble
// per weight, which is the only one appendHuffWeights writes. Below 128 the
// header is the size of an FSE-compressed weight stream.
func (d *decoder) readHuffTable(src []byte) (int, error) {
	if len(src) == 0 {
		return 0, errCorruptLiterals
	}
	header := int(src[0])
	var weights []byte
	var n int
	if header >= 128 {
		count := header - 127
		n = 1 + (count+1)/2
		if len(src) < n {
			return 0, errCorruptLiterals
		}
		weights = d.weights[:count]
		for i := range weights {
			b := src[1+i/2]
			if i&1 == 0 {
				weights[i] = b >> 4
			} else {
				weights[i] = b & 15
			}
		}
	} else {
		n = 1 + header
		if len(src) < n {
			return 0, errCorruptLiterals
		}
		var err error
		if weights, err = d.readFSEWeights(src[1:n]); err != nil {
			return 0, err
		}
	}
	return n, d.huff.build(weights)
}

// readFSEWeights decodes FSE-compressed Huffman weights.
//
// Two states take turns over one bitstream, and there is no count: decoding
// runs until a state update reads past the start of the stream, and then the
// other state contributes one final weight. That termination rule is the
// reference decoder's, reproduced exactly because a weight more or less moves
// every code.
func (d *decoder) readFSEWeights(src []byte) ([]byte, error) {
	norm := d.norm[:maxHuffBits+1]
	n, tableLog, symbols, err := readFSEDescription(src, norm, maxHuffWeightLog)
	if err != nil {
		return nil, err
	}
	t := &d.weightTable
	if err := t.build(norm[:symbols], tableLog); err != nil {
		return nil, err
	}
	var br backReader
	if err := br.init(src[n:]); err != nil {
		return nil, err
	}
	states := [2]uint32{br.bits(tableLog), br.bits(tableLog)}
	br.fill()
	if br.overread() {
		return nil, errCorruptLiterals
	}

	// At most 255 weights: the 256th symbol's weight is always implied.
	w := d.weights[:0]
	for turn := 0; ; turn ^= 1 {
		if len(w) > len(d.weights)-2 {
			return nil, errCorruptLiterals
		}
		e := t.entries[states[turn]]
		w = append(w, e.symbol)
		states[turn] = uint32(e.newState) + br.bits(e.nbBits)
		br.fill()
		if br.overread() {
			return append(w, t.entries[states[turn^1]].symbol), nil
		}
	}
}

// build fills t from the weights of symbols 0 through len(weights)-1. The last
// symbol's weight is not transmitted: the weights must sum to a power of two,
// and it is whatever completes the sum.
func (t *huffDecTable) build(weights []byte) error {
	var rankCount [maxHuffBits + 1]uint32
	var total uint32
	for _, w := range weights {
		if w > maxHuffBits {
			return errCorruptLiterals
		}
		rankCount[w]++
		if w > 0 {
			total += 1 << (w - 1)
		}
	}
	if total == 0 {
		return errCorruptLiterals
	}
	maxBits := uint8(bits.Len32(total))
	if maxBits > maxHuffBits {
		return errCorruptLiterals
	}
	left := uint32(1)<<maxBits - total
	if left&(left-1) != 0 {
		return errCorruptLiterals
	}
	lastWeight := uint8(bits.Len32(left))
	rankCount[lastWeight]++

	// The longest codes come in pairs in any complete prefix code; the
	// reference decoder refuses a table where they do not, and so does this.
	if rankCount[1] < 2 || rankCount[1]&1 != 0 {
		return errCorruptLiterals
	}

	// Canonical layout: all weight-1 symbols first, then weight 2, and so on,
	// each in symbol order, a symbol of weight w taking 2^(w-1) slots.
	var next [maxHuffBits + 1]uint32
	var pos uint32
	for w := 1; w <= int(maxBits); w++ {
		next[w] = pos
		pos += rankCount[w] << (w - 1)
	}
	for s := 0; s <= len(weights); s++ {
		w := lastWeight
		if s < len(weights) {
			w = weights[s]
		}
		if w == 0 {
			continue
		}
		e := huffDecEntry{symbol: byte(s), nbBits: maxBits + 1 - w}
		end := next[w] + 1<<(w-1)
		for i := next[w]; i < end; i++ {
			t.entries[i] = e
		}
		next[w] = end
	}
	t.maxBits = maxBits
	return nil
}

// decodeStream decodes exactly len(dst) literals from one stream, which must
// then be exhausted to the bit.
func (t *huffDecTable) decodeStream(dst, src []byte) error {
	var br backReader
	if err := br.init(src); err != nil {
		return err
	}
	shift := 64 - uint(t.maxBits)
	i := 0
	// A fill leaves at least 56 bits, and four codes take at most 44.
	for ; i+4 <= len(dst); i += 4 {
		br.fill()
		for k := range 4 {
			e := t.entries[br.value<<br.consumed>>shift]
			dst[i+k] = e.symbol
			br.consumed += uint(e.nbBits)
		}
	}
	for ; i < len(dst); i++ {
		br.fill()
		e := t.entries[br.value<<br.consumed>>shift]
		dst[i] = e.symbol
		br.consumed += uint(e.nbBits)
	}
	br.fill()
	if !br.finished() {
		return errCorruptLiterals
	}
	return nil
}

// decode4 decodes the four-stream layout: a jump table giving the sizes of the
// first three streams, then the streams, the first three a quarter of the
// literals each, rounded up, and the fourth the remainder.
func (t *huffDecTable) decode4(dst, src []byte) error {
	if len(src) < 6 {
		return errCorruptLiterals
	}
	var sizes [4]int
	rest := len(src) - 6
	for i := range 3 {
		sizes[i] = int(binary.LittleEndian.Uint16(src[2*i:]))
		rest -= sizes[i]
	}
	if rest < 0 {
		return errCorruptLiterals
	}
	sizes[3] = rest

	segment := (len(dst) + 3) / 4
	in := src[6:]
	out := dst
	for i, size := range sizes {
		part := out
		if i < 3 {
			part = out[:segment]
		}
		if err := t.decodeStream(part, in[:size]); err != nil {
			return err
		}
		out = out[len(part):]
		in = in[size:]
	}
	return nil
}
