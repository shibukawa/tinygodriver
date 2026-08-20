//go:build tinygo || force_tinygo_logic

// Match finding and the sequences section of a compressed block.
//
// The encoder emits many sequences per block. An earlier version emitted one,
// which made its output the input length minus the single longest match: 14 KiB
// of HTML with 200 near-identical lines came out at 14125 bytes. Multiple
// sequences are what turn that into real compression, and they are only
// affordable because Predefined_Mode costs no table bytes; see fse.go.
//
// Matching stays inside the current block, which is what bounds memory to one
// retained block. Matches therefore never reach back into earlier blocks even
// though the format's window would allow it.

package zstd

import (
	"encoding/binary"
	"math/bits"
)

const (
	// minMatch is the shortest match worth a sequence. The format allows 3; 4
	// lets the finder hash and compare a single 32-bit word.
	minMatch = 4

	// maxSequences bounds the per-block sequence list. Reaching it ends the
	// block early and the remainder becomes the next one, so nothing is lost but
	// the matches that would have crossed the boundary.
	maxSequences = 4096
)

// sequence is a run of literals followed by a match, in the format's own terms.
type sequence struct {
	litLen   uint32 // literals preceding the match
	matchLen uint32 // match length, at least minMatch
	offset   uint32 // distance back to the match, at least 1
	ofValue  uint32 // what goes on the wire; see applyRepeatOffsets
}

// seqCode is the three symbol codes one sequence encodes as. The histogram pass
// has to derive them anyway, so they are kept and the bitstream pass reads them
// back rather than deriving them a second time; the widths and extra bits they
// imply are a table lookup and a subtraction away.
type seqCode struct {
	ll, of, ml uint8
}

// applyRepeatOffsets fills each sequence's ofValue.
//
// The format reserves Offset_Values 1 to 3 for the three offsets used most
// recently, and those cost no extra bits, where an explicit offset costs one
// extra bit per bit of distance. That matters more than it sounds: consecutive
// lines of markup or JSON repeat at a constant distance, so most matches in a
// structured document repeat the previous offset, and paying six or seven bits
// each to say so again was the single largest line item in the sequences
// section.
//
// Only Offset_Value 1 is used, and only when literals precede the match. The
// other two slots, and the special meanings all three take on when a match
// follows another match with no literals between them, would each need the full
// three-slot history to stay in step with the decoder for a smaller return.
//
// The decoder resets its repeat slots to 1, 4, 8 at frame start and nowhere
// else: the history runs across every compressed block of the frame, and raw
// and RLE blocks leave it untouched. rep1 is therefore the caller's to thread
// -- slot one as the decoder will hold it when it reaches these sequences --
// and the return value is slot one after them, to be carried into the next
// compressed block that actually reaches the wire. A block the encoder
// abandons must not advance it, because the decoder never executes an
// abandoned block's sequences.
func applyRepeatOffsets(seqs []sequence, rep1 uint32) uint32 {
	for i := range seqs {
		if seqs[i].offset == rep1 && seqs[i].litLen != 0 {
			seqs[i].ofValue = 1
			continue
		}
		// An explicit offset is stored three higher, which is what pushes it clear
		// of the reserved values, and it becomes the new slot one.
		seqs[i].ofValue = seqs[i].offset + 3
		rep1 = seqs[i].offset
	}
	return rep1
}

func hash4(p []byte, i int) uint32 {
	return (binary.LittleEndian.Uint32(p[i:]) * 2654435761) >> (32 - matchTableBits)
}

// extendMatch returns how far p[cand:] and p[pos:] agree, given that the first
// length bytes already do. Eight bytes are compared per step while a full word
// remains on the pos side; cand is always the smaller index, so a word there is
// covered too. The mismatching word's trailing zero count is the number of
// bytes that still agreed, which keeps the result exactly what the byte-at-a-
// time loop would have found.
func extendMatch(p []byte, cand, pos, length int) int {
	for pos+length+8 <= len(p) {
		x := binary.LittleEndian.Uint64(p[pos+length:]) ^ binary.LittleEndian.Uint64(p[cand+length:])
		if x != 0 {
			return length + bits.TrailingZeros64(x)>>3
		}
		length += 8
	}
	for pos+length < len(p) && p[cand+length] == p[pos+length] {
		length++
	}
	return length
}

// recordTouch remembers a match-table slot so the next scan can clear just the
// slots this one dirtied. Once the list fills, clearing the whole table is the
// cheaper option anyway, so tracking simply stops.
func (z *Writer) recordTouch(h uint32) {
	if z.matchAllDirty {
		return
	}
	if len(z.matchTouched) == matchTableSize {
		z.matchAllDirty = true
		return
	}
	z.matchTouched = append(z.matchTouched, uint16(h))
}

// findSequences fills z.seqs describing a prefix of p, and returns how many
// bytes that prefix covers. It is greedy and keeps one candidate per hash: the
// most recent position, which for the repetitive structure of markup and JSON is
// usually the right one.
//
// The returned length is len(p) unless the sequence list filled up first, in
// which case it stops at the end of the last match.
func (z *Writer) findSequences(p []byte) int {
	// A sequence covers at least minMatch bytes, which bounds how many this
	// input can produce; sizing to that bound up front replaces a growth chain
	// with one allocation the Writer then keeps.
	n := len(p)/minMatch + 1
	if n > maxSequences {
		n = maxSequences
	}
	if cap(z.seqs) < n {
		z.seqs = make([]sequence, 0, n)
	}
	z.seqs = z.seqs[:0]

	// The table only ever needs the slots the previous scan wrote cleared, and a
	// small input dirties a fraction of them. The previous scan left either the
	// list of those slots or, when it wrote more slots than the table holds
	// entries, a marker that the whole table is dirty.
	if z.matchAllDirty {
		for i := range z.match {
			z.match[i] = 0
		}
	} else {
		for _, h := range z.matchTouched {
			z.match[h] = 0
		}
	}
	z.matchTouched = z.matchTouched[:0]
	// A scan over a block-sized input writes roughly one slot per position, so
	// tracking them individually would only recreate the full clear with extra
	// steps; declare the table dirty up front and skip the bookkeeping.
	z.matchAllDirty = len(p) >= matchTableSize
	track := !z.matchAllDirty
	if track && cap(z.matchTouched) == 0 {
		z.matchTouched = make([]uint16, 0, matchTableSize)
	}

	if len(p) < minMatch {
		return len(p)
	}

	// Positions are stored one-based so that a zero entry means "empty".
	lit := 0
	pos := 0
	for pos+minMatch <= len(p) {
		h := hash4(p, pos)
		cand := int(z.match[h]) - 1
		z.match[h] = int32(pos + 1)
		if track {
			z.recordTouch(h)
		}
		if cand < 0 || binary.LittleEndian.Uint32(p[cand:]) != binary.LittleEndian.Uint32(p[pos:]) {
			pos++
			continue
		}

		length := extendMatch(p, cand, pos, minMatch)

		// Lazy step: a match one byte later may run longer, and one more literal
		// costs far less than a sequence whose match was cut short. Only the very
		// next position is tried, which is where nearly all of the gain is.
		if pos+minMatch+1 <= len(p) {
			nh := hash4(p, pos+1)
			if next := int(z.match[nh]) - 1; next >= 0 &&
				binary.LittleEndian.Uint32(p[next:]) == binary.LittleEndian.Uint32(p[pos+1:]) {
				nl := extendMatch(p, next, pos+1, minMatch)
				// Strictly longer is the whole test. Requiring it to beat the
				// current match by more than the literal deferring adds was
				// measured, and costs more on structured content than it saves on
				// prose.
				if nl > length {
					z.match[nh] = int32(pos + 2)
					if track {
						z.recordTouch(nh)
					}
					pos++
					cand, length = next, nl
				}
			}
		}

		z.seqs = append(z.seqs, sequence{
			litLen:   uint32(pos - lit),
			matchLen: uint32(length),
			offset:   uint32(pos - cand),
		})

		// Index the interior of the match so a later position can match into it.
		// Without this, long matches leave holes the finder cannot see back into.
		for k := 1; k < length && pos+k+minMatch <= len(p); k++ {
			ih := hash4(p, pos+k)
			z.match[ih] = int32(pos + k + 1)
			if track {
				z.recordTouch(ih)
			}
		}
		pos += length
		lit = pos

		if len(z.seqs) == maxSequences {
			return pos
		}
	}
	return len(p)
}

// appendSequenceCountHeader encodes Number_of_Sequences.
func appendSequenceCountHeader(dst []byte, n int) []byte {
	switch {
	case n < 128:
		return append(dst, byte(n))
	case n < 0x7f00:
		return append(dst, byte(128+n>>8), byte(n))
	default:
		v := n - 0x7f00
		return append(dst, 255, byte(v), byte(v>>8))
	}
}

// appendSequences writes the sequences section: the count, the
// Symbol_Compression_Modes byte, whatever table descriptions that byte promises,
// and the interleaved bitstream.
//
// The bitstream is read backwards, so it is built from the last sequence to the
// first. The final sequence contributes only its extra bits, because the three
// FSE states are seated for it and written at the very end where the decoder
// finds them first.
func (z *Writer) appendSequences(dst []byte) []byte {
	seqs := z.seqs

	// Histogram the three symbol streams so the tables can be fitted to them,
	// keeping each sequence's codes for the bitstream pass below.
	llCounts, ofCounts, mlCounts := &z.llCounts, &z.ofCounts, &z.mlCounts
	*llCounts = [36]uint32{}
	*ofCounts = [32]uint32{}
	*mlCounts = [53]uint32{}
	if cap(z.seqCodes) < len(seqs) {
		z.seqCodes = make([]seqCode, len(seqs))
	}
	codes := z.seqCodes[:len(seqs)]
	maxLL, maxOF, maxML := 0, 0, 0
	for i := range seqs {
		s := &seqs[i]
		ll := literalLengthCode(int(s.litLen))
		ml := matchLengthCode(int(s.matchLen))
		of := uint8(bits.Len32(s.ofValue) - 1)
		codes[i] = seqCode{ll: ll, of: of, ml: ml}
		llCounts[ll]++
		ofCounts[of]++
		mlCounts[ml]++
		if int(ll) > maxLL {
			maxLL = int(ll)
		}
		if int(of) > maxOF {
			maxOF = int(of)
		}
		if int(ml) > maxML {
			maxML = int(ml)
		}
	}

	llT := fitTable(llCounts[:], maxLL, len(seqs), maxLiteralLengthLog,
		ctableLiteralLength, z.llNorm[:], &z.llCT)
	ofT := fitTable(ofCounts[:], maxOF, len(seqs), maxOffsetLog,
		ctableOffset, z.ofNorm[:], &z.ofCT)
	mlT := fitTable(mlCounts[:], maxML, len(seqs), maxMatchLengthLog,
		ctableMatchLength, z.mlNorm[:], &z.mlCT)

	dst = appendSequenceCountHeader(dst, len(seqs))
	dst = append(dst, llT.mode<<6|ofT.mode<<4|mlT.mode<<2)
	// Descriptions follow the byte in literal-length, offset, match-length order.
	for _, t := range [3]*seqTable{&llT, &ofT, &mlT} {
		switch t.mode {
		case modeRLE:
			dst = append(dst, t.rle)
		case modeFSE:
			dst = appendTableDescription(dst, t.norm, t.tableLog)
		}
	}

	bw := bitWriter{out: dst}

	last := &seqs[len(seqs)-1]
	lastCode := codes[len(seqs)-1]
	llBits := literalLengthBits[lastCode.ll]
	mlBits := matchLengthBits[lastCode.ml]

	var llState, ofState, mlState fseState
	llState.init(llT.ct, lastCode.ll)
	ofState.init(ofT.ct, lastCode.of)
	mlState.init(mlT.ct, lastCode.ml)

	bw.addBits(last.litLen-uint32(literalLengthBases[lastCode.ll]), llBits)
	bw.addBits(last.matchLen-uint32(matchLengthBases[lastCode.ml]), mlBits)
	bw.flush32()
	bw.addBits(last.ofValue-1<<lastCode.of, lastCode.of)

	for i := len(seqs) - 2; i >= 0; i-- {
		s := &seqs[i]
		c := codes[i]
		llBits = literalLengthBits[c.ll]
		mlBits = matchLengthBits[c.ml]

		bw.flush32()
		ofState.encode(&bw, c.of)
		mlState.encode(&bw, c.ml)
		llState.encode(&bw, c.ll)

		// The decoder reads literal length, then match length, then offset, so
		// the extra bits go out in that order: lowest bits first.
		extra := uint64(s.ofValue - 1<<c.of)
		extra = extra<<mlBits | uint64(s.matchLen-uint32(matchLengthBases[c.ml]))
		extra = extra<<llBits | uint64(s.litLen-uint32(literalLengthBases[c.ll]))
		width := llBits + mlBits + c.of

		bw.flush32()
		if width <= 31 {
			bw.addBits64(extra, width)
		} else {
			bw.addBits64(extra, 32)
			bw.flush32()
			bw.addBits64(extra>>32, width-32)
		}
	}

	mlState.flush(&bw)
	ofState.flush(&bw)
	llState.flush(&bw)
	bw.close()
	return bw.out
}

// appendCompressedBlock builds a complete compressed block for p, whose
// sequences are already in z.seqs, and reports whether it came out smaller than
// rleSize, what the stored fallback would take. The caller falls back when it
// did not.
func (z *Writer) appendCompressedBlock(dst []byte, p []byte, last bool, rleSize int) ([]byte, bool) {
	if len(z.seqs) == 0 {
		return dst, false
	}

	// Literals are every byte no match covered, in order. Each stretch is
	// histogrammed as it is gathered, while it is still warm, so the literal
	// coder does not walk the whole buffer a second time.
	if cap(z.literals) < len(p) {
		z.literals = make([]byte, 0, len(p))
	}
	z.literals = z.literals[:0]
	z.litCounts = [256]uint32{}
	at := 0
	for i := range z.seqs {
		s := &z.seqs[i]
		chunk := p[at : at+int(s.litLen)]
		z.literals = append(z.literals, chunk...)
		for _, b := range chunk {
			z.litCounts[b]++
		}
		at += int(s.litLen) + int(s.matchLen)
	}
	z.literals = append(z.literals, p[at:]...)
	for _, b := range p[at:] {
		z.litCounts[b]++
	}
	z.litCountsReady = true
	rep1 := applyRepeatOffsets(z.seqs, z.rep1)

	start := len(dst)
	dst = append(dst, 0, 0, 0) // Block_Header, rewritten once the size is known
	dst = z.appendLiterals(dst)
	dst = z.appendSequences(dst)

	size := len(dst) - start - 3
	// rleSize counts the block headers the stored fallback would need, so this
	// block's own three must be counted too or a compressed block wins
	// comparisons it loses.
	if size+3 >= rleSize || size >= 1<<21 {
		return dst[:start], false
	}
	header := uint32(size)<<3 | 2<<1 // Block_Type 2: compressed
	if last {
		header |= 1
	}
	dst[start] = byte(header)
	dst[start+1] = byte(header >> 8)
	dst[start+2] = byte(header >> 16)
	// Only now is the block certain to reach the wire, so only now may the
	// decoder-visible repeat history advance. The refusals above fall back to
	// raw and RLE blocks, which a decoder replays without touching its slots.
	z.rep1 = rep1
	return dst, true
}
