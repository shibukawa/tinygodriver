//go:build tinygo || force_tinygo_logic

// Huffman coding for the literals section.
//
// Literals were stored raw until the lazy match step lengthened the matches, at
// which point they became the largest single cost in a block -- 688 bytes against
// 750 of sequences on a 14 KiB HTML listing. Coding them is what turns that
// around.
//
// Only the direct weight representation is written, never FSE-compressed weights.
// It costs one nibble per symbol below the largest used one, which for the
// digit-and-punctuation alphabets that HTML and JSON literals actually consist of
// is a few dozen bytes, and it removes an entire second entropy coder from a
// package whose point is to stay small. The cost is that the largest literal byte
// must be 128 or below, since the header byte encodes the weight count as
// 127 plus it; a block whose literals reach higher falls back to storing them.

package zstd

const (
	// maxHuffBits is the longest literal code the format permits.
	maxHuffBits = 11

	// maxHuffSymbol is the largest literal byte the direct weight representation
	// can describe, because the header byte is 127 plus the number of weights.
	maxHuffSymbol = 128

	// minHuffLiterals is the point below which a tree cannot pay for itself.
	minHuffLiterals = 32

	// fourStreamLiterals is where the format's single-stream size fields run out
	// and the four-stream layout takes over.
	fourStreamLiterals = 1024
)

// Literals_Block_Type values.
const (
	literalsRaw        = 0
	literalsRLE        = 1
	literalsCompressed = 2
)

// huffTable is a canonical Huffman code over literal bytes. The per-symbol
// arrays span every byte value even though codes stop at maxHuffSymbol, so the
// stream coder can index them with a byte directly.
type huffTable struct {
	bits   [256]uint8  // code length per symbol, 0 when absent
	vals   [256]uint16 // code value per symbol
	max    int         // largest symbol with a code
	maxBit uint8       // longest code length
}

// huffNode is one tree node: leaves first, then internal nodes appended as they
// are created. Indices and symbols both fit comfortably in 16 bits.
type huffNode struct {
	weight      uint64
	left, right int16 // -1 for a leaf
	symbol      int16
}

// huffScratch is the tree builder's working set, sized for the largest tree the
// direct weight representation can describe: 129 leaves and 128 merges. It
// lives on the Writer so building a tree, which can happen four times per
// block, allocates nothing.
type huffScratch struct {
	nodes [2 * (maxHuffSymbol + 1)]huffNode
	live  [maxHuffSymbol + 1]int16
	stack [2 * (maxHuffSymbol + 1)]huffFrame
}

type huffFrame struct {
	n     int16
	depth uint8
}

// buildHuffTable fits a code to counts, writing it into t. It reports false
// when the alphabet cannot be described: fewer than two symbols, a symbol above
// what the direct weight representation reaches, or a tree deeper than the
// format allows even after the rare counts have been lifted.
func buildHuffTable(t *huffTable, sc *huffScratch, counts *[256]uint32, total int) bool {
	maxSymbol := -1
	used := 0
	for s, c := range counts {
		if c == 0 {
			continue
		}
		used++
		maxSymbol = s
	}
	// Two symbols are the minimum: a tree needs an even number of longest codes,
	// at least two of them, and one symbol has no tree at all. One symbol is an
	// RLE literals block, which the caller handles.
	if used < 2 || maxSymbol < 1 || maxSymbol > maxHuffSymbol {
		return false
	}

	// Lifting the rarest counts bounds the tree's depth. A first attempt uses the
	// counts as they are; each retry lifts the floor, which flattens the code and
	// costs a fraction of a bit on common symbols to keep the rare ones in reach.
	var lengths [maxHuffSymbol + 1]uint8
	ok := false
	for _, shift := range [...]uint{31, 11, 9, 7} {
		floor := uint32(total) >> shift
		if floor < 1 {
			floor = 1
		}
		if huffLengths(sc, counts, maxSymbol, floor, &lengths) <= maxHuffBits {
			ok = true
			break
		}
	}
	if !ok {
		return false
	}

	*t = huffTable{max: maxSymbol}
	for s := 0; s <= maxSymbol; s++ {
		t.bits[s] = lengths[s]
		if lengths[s] > t.maxBit {
			t.maxBit = lengths[s]
		}
	}

	// Canonical values, walking from the longest code up, which is the order the
	// format's decoder reconstructs them in.
	var perLength [maxHuffBits + 2]uint16
	for s := 0; s <= maxSymbol; s++ {
		if t.bits[s] != 0 {
			perLength[t.bits[s]]++
		}
	}
	var next [maxHuffBits + 2]uint16
	min := uint16(0)
	for n := int(t.maxBit); n > 0; n-- {
		next[n] = min
		min += perLength[n]
		min >>= 1
	}
	for s := 0; s <= maxSymbol; s++ {
		if n := t.bits[s]; n != 0 {
			t.vals[s] = next[n]
			next[n]++
		}
	}
	return true
}

// huffLengths assigns code lengths by repeated merging of the two smallest
// weights, and returns the longest length produced. counts below floor are
// treated as floor.
//
// The alphabet is at most 129 symbols, so merging in place over a slice costs
// little and is far harder to get wrong than a heap.
func huffLengths(sc *huffScratch, counts *[256]uint32, maxSymbol int, floor uint32, out *[maxHuffSymbol + 1]uint8) uint8 {
	nodes := sc.nodes[:0]
	live := sc.live[:0]
	for s := 0; s <= maxSymbol; s++ {
		c := counts[s]
		if c == 0 {
			continue
		}
		if c < floor {
			c = floor
		}
		nodes = append(nodes, huffNode{weight: uint64(c), left: -1, right: -1, symbol: int16(s)})
		live = append(live, int16(len(nodes)-1))
	}

	for len(live) > 1 {
		// Two smallest, by weight and then by index so the result is stable.
		a, b := 0, 1
		if nodes[live[b]].weight < nodes[live[a]].weight {
			a, b = b, a
		}
		for i := 2; i < len(live); i++ {
			switch w := nodes[live[i]].weight; {
			case w < nodes[live[a]].weight:
				b, a = a, i
			case w < nodes[live[b]].weight:
				b = i
			}
		}
		nodes = append(nodes, huffNode{
			weight: nodes[live[a]].weight + nodes[live[b]].weight,
			left:   live[a],
			right:  live[b],
			symbol: -1,
		})
		// Replace one slot with the parent and remove the other.
		lo, hi := a, b
		if lo > hi {
			lo, hi = hi, lo
		}
		live[lo] = int16(len(nodes) - 1)
		live[hi] = live[len(live)-1]
		live = live[:len(live)-1]
	}

	*out = [maxHuffSymbol + 1]uint8{}
	var deepest uint8
	// Depth-first walk, carrying the depth. The tree has at most 257 nodes, so an
	// explicit stack is bounded and cheap.
	stack := sc.stack[:0]
	stack = append(stack, huffFrame{n: live[0], depth: 0})
	for len(stack) > 0 {
		f := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		n := nodes[f.n]
		if n.left < 0 {
			d := f.depth
			if d == 0 {
				d = 1 // a single-leaf tree still needs a bit
			}
			out[n.symbol] = d
			if d > deepest {
				deepest = d
			}
			continue
		}
		stack = append(stack, huffFrame{n: n.left, depth: f.depth + 1})
		stack = append(stack, huffFrame{n: n.right, depth: f.depth + 1})
	}
	return deepest
}

// appendHuffWeights writes the tree description.
//
// A weight is the longest code length plus one, less this symbol's length, so the
// longest code has weight 1 and an absent symbol has weight 0. The largest
// symbol's weight is not written: the decoder derives it from the fact that the
// weights must sum to a power of two.
func appendHuffWeights(dst []byte, t *huffTable) []byte {
	n := t.max // weights for symbols 0 .. max-1; max itself is implied
	dst = append(dst, byte(127+n))
	for i := 0; i < n; i += 2 {
		hi := huffWeight(t, i)
		lo := byte(0)
		if i+1 < n {
			lo = huffWeight(t, i+1)
		}
		dst = append(dst, hi<<4|lo)
	}
	return dst
}

func huffWeight(t *huffTable, symbol int) byte {
	if t.bits[symbol] == 0 {
		return 0
	}
	return t.maxBit + 1 - t.bits[symbol]
}

// appendHuffStream codes lits into one stream.
//
// The literals go in backwards. A Huffman stream is read from its end, the same
// way a sequences bitstream is, so the last literal has to be written first for
// the decoder to hand them back in order.
//
// Two literals go between flushes: a flush leaves at most 31 bits pending, and
// two codes of at most 11 bits each keep the container within its 64, where a
// third could not be guaranteed to. Flushing emits the same bytes in the same
// order however often it runs, so the stream is unchanged by the batching.
func appendHuffStream(dst []byte, lits []byte, t *huffTable) []byte {
	bw := bitWriter{out: dst}
	i := len(lits) - 1
	for ; i >= 1; i -= 2 {
		c0, c1 := lits[i], lits[i-1]
		bw.addBits(uint32(t.vals[c0]), t.bits[c0])
		bw.addBits(uint32(t.vals[c1]), t.bits[c1])
		bw.flush32()
	}
	if i == 0 {
		c := lits[0]
		bw.addBits(uint32(t.vals[c]), t.bits[c])
	}
	bw.close()
	return bw.out
}

// appendRawLiterals is the stored form the coded choices are measured against
// and fall back to.
func appendRawLiterals(dst, lits []byte) []byte {
	dst = appendLiteralHeader(dst, len(lits), literalsRaw)
	return append(dst, lits...)
}

// appendLiterals writes the literals section, choosing the cheapest of storing
// them, an RLE byte, and a Huffman code. A choice that would not actually be
// smaller is not taken, so this can only help.
func (z *Writer) appendLiterals(dst []byte) []byte {
	lits := z.literals
	counted := z.litCountsReady
	z.litCountsReady = false
	if len(lits) == 0 {
		return appendLiteralHeader(dst, 0, literalsRaw)
	}

	// The block builder histograms the literals as it gathers them; a caller
	// that filled z.literals itself has not.
	if !counted {
		z.litCounts = [256]uint32{}
		for _, c := range lits {
			z.litCounts[c]++
		}
	}

	// One distinct byte: the whole run is the header plus that byte.
	if int(z.litCounts[lits[0]]) == len(lits) {
		dst = appendLiteralHeader(dst, len(lits), literalsRLE)
		return append(dst, lits[0])
	}

	if len(lits) < minHuffLiterals {
		return appendRawLiterals(dst, lits)
	}
	if !buildHuffTable(&z.huffTab, &z.huffSc, &z.litCounts, len(lits)) {
		return appendRawLiterals(dst, lits)
	}
	t := &z.huffTab

	// Build the body -- tree description, then one or four streams -- into scratch
	// so its size is known before the header that has to state it.
	z.litBody = z.litBody[:0]
	z.litBody = appendHuffWeights(z.litBody, t)
	single := len(lits) < fourStreamLiterals
	if single {
		z.litBody = appendHuffStream(z.litBody, lits, t)
	} else {
		jump := len(z.litBody)
		z.litBody = append(z.litBody, 0, 0, 0, 0, 0, 0)
		segment := (len(lits) + 3) / 4
		rest := lits
		for i := range 4 {
			part := rest
			if len(part) > segment {
				part = part[:segment]
			}
			rest = rest[len(part):]
			at := len(z.litBody)
			z.litBody = appendHuffStream(z.litBody, part, t)
			size := len(z.litBody) - at
			if size > 0xFFFF {
				return appendRawLiterals(dst, lits) // the jump table cannot state it
			}
			if i < 3 {
				z.litBody[jump+i*2] = byte(size)
				z.litBody[jump+i*2+1] = byte(size >> 8)
			}
		}
	}

	var header [5]byte
	n, ok := compressedLiteralHeader(&header, len(lits), len(z.litBody), single)
	if !ok || n+len(z.litBody) >= literalHeaderSize(len(lits))+len(lits) {
		return appendRawLiterals(dst, lits)
	}
	dst = append(dst, header[:n]...)
	return append(dst, z.litBody...)
}

// compressedLiteralHeader states the decoded and encoded sizes, in the narrowest
// of the format's three widths that holds them, writing into hdr and returning
// how many of its bytes are used.
func compressedLiteralHeader(hdr *[5]byte, regen, comp int, single bool) (int, bool) {
	switch {
	case regen < 1<<10 && comp < 1<<10:
		format := uint32(1) // four streams
		if single {
			format = 0
		}
		v := uint32(literalsCompressed) | format<<2 | uint32(regen)<<4 | uint32(comp)<<14
		hdr[0], hdr[1], hdr[2] = byte(v), byte(v>>8), byte(v>>16)
		return 3, true
	case single:
		// The single-stream layout has nowhere to put sizes this large.
		return 0, false
	case regen < 1<<14 && comp < 1<<14:
		v := uint32(literalsCompressed) | 2<<2 | uint32(regen)<<4 | uint32(comp)<<18
		hdr[0], hdr[1], hdr[2], hdr[3] = byte(v), byte(v>>8), byte(v>>16), byte(v>>24)
		return 4, true
	case regen < 1<<18 && comp < 1<<18:
		v := uint64(literalsCompressed) | 3<<2 | uint64(regen)<<4 | uint64(comp)<<22
		hdr[0], hdr[1], hdr[2], hdr[3], hdr[4] = byte(v), byte(v>>8), byte(v>>16), byte(v>>24), byte(v>>32)
		return 5, true
	default:
		return 0, false
	}
}
