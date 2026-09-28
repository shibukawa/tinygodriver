//go:build tinygo || force_tinygo_logic

// FSE decoding: the backward bitstream both entropy coders share, table
// descriptions as they arrive on the wire, and the decoding tables built from
// them.
//
// The encoder writes only what it needs -- no -1 probabilities, no
// FSE-compressed Huffman weights -- but a decoder meets whatever the reference
// encoder chose, so everything here takes the format's full range and treats
// every field as untrusted.

package zstd

import (
	"encoding/binary"
	"math/bits"
)

// backReader reads a bitstream from its end, which is how both the sequences
// section and Huffman streams are laid out: the encoder's last bit is the
// decoder's first.
//
// value holds up to eight bytes of the stream with the most recently written
// bits on top; consumed counts how many of its 64 bits have been read from the
// top down. Bits past the start of the stream read as zero and still count, so
// an overread shows up as consumed passing 64 once off reaches 0 rather than as
// a panic -- callers check with overread or finished where the format says the
// stream must end.
type backReader struct {
	in       []byte
	off      int // bytes in[:off] are not yet loaded into value
	value    uint64
	consumed uint
}

// init seats the reader at the end marker: the highest set bit of the last
// byte, which is padding and not part of the data.
func (b *backReader) init(in []byte) error {
	if len(in) == 0 {
		return errCorruptBitstream
	}
	last := in[len(in)-1]
	if last == 0 {
		return errCorruptBitstream
	}
	b.in = in
	b.off = len(in)
	b.value = 0
	b.consumed = 64
	b.fill()
	// fill loaded the marker byte on top of whatever else fitted; skip its
	// leading zeros and the marker bit itself.
	b.consumed += uint(bits.LeadingZeros8(last)) + 1
	return nil
}

// fill loads bytes until at least 56 bits are unread or the stream has no more
// bytes. Every read here is at most 32 bits, and a sequence's three state
// updates at most 26, so one fill before each group is always enough.
func (b *backReader) fill() {
	if b.consumed >= 32 && b.off >= 4 {
		b.off -= 4
		b.value = b.value<<32 | uint64(binary.LittleEndian.Uint32(b.in[b.off:]))
		b.consumed -= 32
	}
	for b.consumed >= 8 && b.off > 0 {
		b.off--
		b.value = b.value<<8 | uint64(b.in[b.off])
		b.consumed -= 8
	}
}

// bits reads the next n bits, n at most 32.
func (b *backReader) bits(n uint8) uint32 {
	v := b.peek(n)
	b.consumed += uint(n)
	return v
}

// peek returns the next n bits without consuming them, n at most 32. The shifts
// are deliberately unmasked: Go defines a shift of 64 or more as zero, which is
// what both n == 0 and a reader past its end need.
func (b *backReader) peek(n uint8) uint32 {
	return uint32(b.value << b.consumed >> (64 - uint(n)))
}

// overread reports whether more bits were read than the stream holds.
func (b *backReader) overread() bool {
	return b.off == 0 && b.consumed > 64
}

// finished reports whether exactly every bit of the stream has been read.
func (b *backReader) finished() bool {
	return b.off == 0 && b.consumed == 64
}

// fseDecEntry is one state of a decoding table: the symbol it stands for, and
// the next state, which is newState plus the next nbBits bits of the stream.
type fseDecEntry struct {
	newState uint16
	symbol   uint8
	nbBits   uint8
}

// fseDecTable is a decoding table in storage large enough for any the format
// allows, so a decoder rebuilds its tables every block without allocating.
type fseDecTable struct {
	entries  [1 << maxLiteralLengthLog]fseDecEntry
	tableLog uint8
}

// build fills t from a normalised distribution.
func (t *fseDecTable) build(norm []int16, tableLog uint8) error {
	tableSize := uint32(1) << tableLog
	var tableSymbolArr [1 << maxLiteralLengthLog]byte
	tableSymbol := tableSymbolArr[:tableSize]
	if !spreadSymbols(tableSymbol, norm, tableLog) {
		return errCorruptTable
	}

	// Each occurrence of a symbol takes the next of its states in turn; a
	// symbol with count c numbers them c to 2c-1, and the number decides how
	// many bits reach the state after it.
	var nextArr [maxFSESymbols]uint16
	next := nextArr[:len(norm)]
	for s, v := range norm {
		if v == -1 {
			v = 1
		}
		next[s] = uint16(v)
	}
	for u, sym := range tableSymbol {
		n := next[sym]
		next[sym]++
		nb := tableLog - uint8(bits.Len16(n)-1)
		t.entries[u] = fseDecEntry{
			newState: uint16(uint32(n)<<nb - tableSize),
			symbol:   sym,
			nbBits:   nb,
		}
	}
	t.tableLog = tableLog
	return nil
}

// rle makes t a one-state table that yields symbol and reads nothing.
func (t *fseDecTable) rle(symbol uint8) {
	t.entries[0] = fseDecEntry{symbol: symbol}
	t.tableLog = 0
}

// maxFSESymbols is the largest alphabet any FSE table describes: match length
// codes.
const maxFSESymbols = 53

// readFSEDescription parses a table description from the front of src into
// norm, whose length is the alphabet size. It returns the bytes the
// description took, its accuracy log, and how many symbols it described.
//
// This is the reader for appendTableDescription's format, taken in full: the
// description may use -1 probabilities, which that writer never emits.
func readFSEDescription(src []byte, norm []int16, maxLog uint8) (n int, tableLog uint8, symbols int, err error) {
	if len(src) == 0 {
		return 0, 0, 0, errCorruptTable
	}
	for i := range norm {
		norm[i] = 0
	}
	r := fwdReader{src: src}
	tableLog = uint8(r.bits(4)) + minTableLog
	if tableLog > maxLog {
		return 0, 0, 0, errCorruptTable
	}

	tableSize := int32(1) << tableLog
	remaining := tableSize + 1 // the format's own accounting, as in the writer
	threshold := tableSize
	nbBits := uint(tableLog + 1)
	previous0 := false
	symbol := 0
	for remaining > 1 {
		if previous0 {
			// Two-bit repeat flags, each adding up to three more zero
			// probabilities; a flag of 3 means another flag follows.
			for {
				repeat := int(r.bits(2))
				symbol += repeat
				if repeat != 3 {
					break
				}
			}
		}
		if symbol >= len(norm) {
			return 0, 0, 0, errCorruptTable
		}

		// Values below max fit in one bit fewer; the rest take the full width,
		// the upper half of their range shifted down by max.
		max := (2*threshold - 1) - remaining
		v := int32(r.peek(nbBits))
		if v&(threshold-1) < max {
			v &= threshold - 1
			r.skip(nbBits - 1)
		} else {
			if v >= threshold {
				v -= max
			}
			r.skip(nbBits)
		}

		// The value on the wire is one higher than the probability, and -1
		// means a single state held back for a very rare symbol.
		count := int16(v - 1)
		if count < 0 {
			remaining--
		} else {
			remaining -= int32(count)
		}
		norm[symbol] = count
		symbol++
		previous0 = count == 0
		for remaining < threshold {
			nbBits--
			threshold >>= 1
		}
	}
	n = int((r.pos + 7) / 8)
	if n > len(src) {
		return 0, 0, 0, errCorruptTable
	}
	return n, tableLog, symbol, nil
}

// fwdReader reads a table description's bitstream, least-significant bit
// first. Reading past the end yields zeros, and the caller checks how far it
// went once it knows how much it needed.
type fwdReader struct {
	src []byte
	pos uint
}

// peek returns the next n bits, n at most 17.
func (r *fwdReader) peek(n uint) uint32 {
	i := int(r.pos >> 3)
	var v uint32
	for k := 0; k < 4 && i+k < len(r.src); k++ {
		v |= uint32(r.src[i+k]) << (8 * k)
	}
	return v >> (r.pos & 7) & (1<<n - 1)
}

func (r *fwdReader) skip(n uint) { r.pos += n }

func (r *fwdReader) bits(n uint) uint32 {
	v := r.peek(n)
	r.pos += n
	return v
}
