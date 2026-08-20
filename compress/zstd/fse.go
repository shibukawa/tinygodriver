//go:build tinygo || force_tinygo_logic

// Finite State Entropy encoding for the sequences section, against RFC 8878's
// default distributions.
//
// Predefined_Mode is what makes multiple sequences per block affordable here: the
// tables cost zero bytes on the wire, so the encoder needs no table
// transmission, no distribution normalisation and no accuracy search — just the
// state machine. The three tables together are about 1.3 KiB, built once at
// startup from the distributions below rather than embedded, which keeps them out
// of rodata and lets the three share one builder.

package zstd

import "math/bits"

// Default distributions from RFC 8878 section 3.1.1.3.2.2.1. A -1 marks a
// low-probability symbol, which takes a single state allocated from the top of
// the table.
var (
	predefLiteralLengthNorm = [36]int16{
		4, 3, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 1, 1, 1,
		2, 2, 2, 2, 2, 2, 2, 2, 2, 3, 2, 1, 1, 1, 1, 1,
		-1, -1, -1, -1,
	}
	predefOffsetNorm = [29]int16{
		1, 1, 1, 1, 1, 1, 2, 2, 2, 1, 1, 1, 1, 1, 1, 1,
		1, 1, 1, 1, 1, 1, 1, 1, -1, -1, -1, -1, -1,
	}
	predefMatchLengthNorm = [53]int16{
		1, 4, 3, 2, 2, 2, 2, 2, 2, 1, 1, 1, 1, 1, 1, 1,
		1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1,
		1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, -1, -1,
		-1, -1, -1, -1, -1,
	}
)

const (
	predefLiteralLengthLog = 6
	predefOffsetLog        = 5
	predefMatchLengthLog   = 6
)

// The three predefined tables, built once. A frame is encoded entirely from
// these, so they are shared and never mutated after startup.
var (
	ctableLiteralLength = buildCTable(predefLiteralLengthNorm[:], predefLiteralLengthLog)
	ctableOffset        = buildCTable(predefOffsetNorm[:], predefOffsetLog)
	ctableMatchLength   = buildCTable(predefMatchLengthNorm[:], predefMatchLengthLog)
)

// symbolTransform holds what encoding one symbol costs and where it lands.
// deltaNbBits packs the bit count in its high half and is designed to be added
// to a state and shifted down by 16; the arithmetic relies on uint32 wrapping,
// which is why it is not stored as a plain count.
type symbolTransform struct {
	deltaNbBits    uint32
	deltaFindState int16
}

type ctable struct {
	stateTable []uint16
	symbolTT   []symbolTransform
	tableLog   uint8
}

// ctableScratch is the backing store for one per-block fitted table, sized for
// the largest accuracy and alphabet the format allows here, so refitting a
// table every block allocates nothing.
type ctableScratch struct {
	states [1 << maxLiteralLengthLog]uint16
	tts    [53]symbolTransform
	ct     ctable
}

// fitted points the scratch's table at norm's shape and fills it.
func (c *ctableScratch) fitted(norm []int16, tableLog uint8) *ctable {
	c.ct.stateTable = c.states[:1<<tableLog]
	c.ct.symbolTT = c.tts[:len(norm)]
	fillCTable(&c.ct, norm, tableLog)
	return &c.ct
}

// rle is a table with one state, for a stream that carries one symbol. Its
// accuracy log is zero, so the state machine drives it like any other table and
// writes nothing: no state bits, no final state.
func (c *ctableScratch) rle(symbol int) *ctable {
	c.states[0] = 0
	c.ct.stateTable = c.states[:1]
	c.ct.symbolTT = c.tts[:symbol+1]
	for i := range c.ct.symbolTT {
		c.ct.symbolTT[i] = symbolTransform{}
	}
	c.ct.tableLog = 0
	return &c.ct
}

// buildCTable allocates a table and fills it. It is how the shared predefined
// tables are built at startup; per-block tables go through a ctableScratch
// instead, which reuses one allocation for the life of a Writer.
func buildCTable(norm []int16, tableLog uint8) *ctable {
	ct := &ctable{
		stateTable: make([]uint16, uint32(1)<<tableLog),
		symbolTT:   make([]symbolTransform, len(norm)),
	}
	fillCTable(ct, norm, tableLog)
	return ct
}

// fillCTable turns a normalised distribution into an encoding table, writing
// into ct's presized stateTable and symbolTT. It follows the construction the
// format requires: symbols are spread across the state space with a stride that
// visits every slot exactly once, low-probability symbols are placed from the
// top down, and each symbol's transform records how many state bits it emits.
// Every entry of both tables is written, so reused storage needs no clearing.
func fillCTable(ct *ctable, norm []int16, tableLog uint8) {
	tableSize := uint32(1) << tableLog
	symbolLen := len(norm)

	// Cumulative start position per symbol, and the spread order. The arrays
	// are sized for the largest alphabet and accuracy used here, so they live
	// on the stack.
	var cumulArr [54]int16
	var tableSymbolArr [1 << maxLiteralLengthLog]byte
	cumul := cumulArr[:symbolLen+1]
	tableSymbol := tableSymbolArr[:tableSize]
	highThreshold := tableSize - 1
	for i, v := range norm {
		if v == -1 {
			cumul[i+1] = cumul[i] + 1
			tableSymbol[highThreshold] = byte(i)
			highThreshold--
			continue
		}
		cumul[i+1] = cumul[i] + v
	}
	if uint32(cumul[symbolLen]) != tableSize {
		panic("zstd: predefined distribution does not sum to its table size")
	}
	cumul[symbolLen] = int16(tableSize) + 1

	// Spread the symbols. The stride is coprime with the table size, so the walk
	// touches every position; positions already taken by low-probability symbols
	// are skipped.
	step := tableSize>>1 + tableSize>>3 + 3
	mask := tableSize - 1
	var position uint32
	for i, v := range norm {
		for range v {
			tableSymbol[position] = byte(i)
			position = (position + step) & mask
			for position > highThreshold {
				position = (position + step) & mask
			}
		}
	}
	if position != 0 {
		panic("zstd: symbol spread did not return to its starting position")
	}

	ct.tableLog = tableLog
	for u, sym := range tableSymbol {
		ct.stateTable[cumul[sym]] = uint16(tableSize + uint32(u))
		cumul[sym]++
	}

	total := int16(0)
	tl := (uint32(tableLog) << 16) - tableSize
	for i, v := range norm {
		switch v {
		case 0:
			ct.symbolTT[i] = symbolTransform{}
		case -1, 1:
			ct.symbolTT[i].deltaNbBits = tl
			ct.symbolTT[i].deltaFindState = total - 1
			total++
		default:
			maxBitsOut := uint32(tableLog) - uint32(bits.Len32(uint32(v-1))-1)
			minStatePlus := uint32(v) << maxBitsOut
			ct.symbolTT[i].deltaNbBits = (maxBitsOut << 16) - minStatePlus
			ct.symbolTT[i].deltaFindState = total - v
			total += v
		}
	}
	if total != int16(tableSize) {
		panic("zstd: symbol transforms do not cover the table")
	}
}

// bitWriter accumulates bits least-significant first and emits whole bytes in
// increasing address order. A sequences bitstream is read backwards by the
// decoder, so what this writes last is what the decoder reads first.
type bitWriter struct {
	container uint64
	nBits     uint8
	out       []byte
}

// addBits appends the low n bits of v. The caller must have flushed recently
// enough that n bits fit; flush32 after at most 32 bits guarantees that.
func (b *bitWriter) addBits(v uint32, n uint8) {
	if n == 0 {
		return
	}
	b.container |= uint64(v&(1<<n-1)) << (b.nBits & 63)
	b.nBits += n
}

// addBits64 appends the low n bits of v, for the packed extra-bits word.
func (b *bitWriter) addBits64(v uint64, n uint8) {
	if n == 0 {
		return
	}
	b.container |= (v & (1<<n - 1)) << (b.nBits & 63)
	b.nBits += n
}

func (b *bitWriter) flush32() {
	if b.nBits < 32 {
		return
	}
	b.out = append(b.out,
		byte(b.container),
		byte(b.container>>8),
		byte(b.container>>16),
		byte(b.container>>24))
	b.nBits -= 32
	b.container >>= 32
}

// close writes the end marker the decoder looks for -- a single set bit, whose
// position tells it where the stream's last bit is -- and pads to a byte.
func (b *bitWriter) close() {
	b.addBits(1, 1)
	for n := (b.nBits + 7) / 8; n > 0; n-- {
		b.out = append(b.out, byte(b.container))
		b.container >>= 8
	}
	b.container = 0
	b.nBits = 0
}

// fseState is one of the three interleaved encoder states.
type fseState struct {
	state uint16
	ct    *ctable
}

// init seats the state for the symbol that will be decoded last, which is the
// one encoded first.
func (s *fseState) init(ct *ctable, symbol byte) {
	s.ct = ct
	tt := ct.symbolTT[symbol]
	nbBitsOut := (tt.deltaNbBits + 1<<15) >> 16
	im := int32((nbBitsOut << 16) - tt.deltaNbBits)
	lu := (im >> nbBitsOut) + int32(tt.deltaFindState)
	s.state = ct.stateTable[lu]
}

// encode emits the state bits for symbol and advances to the next state.
func (s *fseState) encode(bw *bitWriter, symbol byte) {
	tt := s.ct.symbolTT[symbol]
	nbBitsOut := (uint32(s.state) + tt.deltaNbBits) >> 16
	dst := int32(s.state>>(nbBitsOut&15)) + int32(tt.deltaFindState)
	bw.addBits(uint32(s.state), uint8(nbBitsOut))
	s.state = s.ct.stateTable[dst]
}

// flush writes the final state, which the decoder reads first to seat itself.
func (s *fseState) flush(bw *bitWriter) {
	bw.flush32()
	bw.addBits(uint32(s.state), s.ct.tableLog)
}
