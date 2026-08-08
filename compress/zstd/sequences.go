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
func applyRepeatOffsets(seqs []sequence) {
	// The decoder starts a frame with 1, 4, 8 in its repeat slots, so the first
	// sequence can only match slot one by having a distance of exactly 1.
	rep1 := uint32(1)
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
}

func hash4(p []byte, i int) uint32 {
	v := uint32(p[i]) | uint32(p[i+1])<<8 | uint32(p[i+2])<<16 | uint32(p[i+3])<<24
	return (v * 2654435761) >> (32 - matchTableBits)
}

// findSequences fills z.seqs describing a prefix of p, and returns how many
// bytes that prefix covers. It is greedy and keeps one candidate per hash: the
// most recent position, which for the repetitive structure of markup and JSON is
// usually the right one.
//
// The returned length is len(p) unless the sequence list filled up first, in
// which case it stops at the end of the last match.
func (z *Writer) findSequences(p []byte) int {
	z.seqs = z.seqs[:0]
	for i := range z.match {
		z.match[i] = 0
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
		if cand < 0 || p[cand] != p[pos] || p[cand+1] != p[pos+1] ||
			p[cand+2] != p[pos+2] || p[cand+3] != p[pos+3] {
			pos++
			continue
		}

		length := minMatch
		for pos+length < len(p) && p[cand+length] == p[pos+length] {
			length++
		}

		// Lazy step: a match one byte later may run longer, and one more literal
		// costs far less than a sequence whose match was cut short. Only the very
		// next position is tried, which is where nearly all of the gain is.
		if pos+minMatch+1 <= len(p) {
			nh := hash4(p, pos+1)
			if next := int(z.match[nh]) - 1; next >= 0 &&
				p[next] == p[pos+1] && p[next+1] == p[pos+2] &&
				p[next+2] == p[pos+3] && p[next+3] == p[pos+4] {
				nl := minMatch
				for pos+1+nl < len(p) && p[next+nl] == p[pos+1+nl] {
					nl++
				}
				// Strictly longer is the whole test. Requiring it to beat the
				// current match by more than the literal deferring adds was
				// measured, and costs more on structured content than it saves on
				// prose.
				if nl > length {
					z.match[nh] = int32(pos + 2)
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
			z.match[hash4(p, pos+k)] = int32(pos + k + 1)
		}
		pos += length
		lit = pos

		if len(z.seqs) == maxSequences {
			return pos
		}
	}
	return len(p)
}

// sequenceCountHeader encodes Number_of_Sequences.
func sequenceCountHeader(n int) []byte {
	switch {
	case n < 128:
		return []byte{byte(n)}
	case n < 0x7f00:
		return []byte{byte(128 + n>>8), byte(n)}
	default:
		v := n - 0x7f00
		return []byte{255, byte(v), byte(v >> 8)}
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

	// Histogram the three symbol streams so the tables can be fitted to them.
	llCounts, ofCounts, mlCounts := &z.llCounts, &z.ofCounts, &z.mlCounts
	*llCounts = [36]uint32{}
	*ofCounts = [32]uint32{}
	*mlCounts = [53]uint32{}
	maxLL, maxOF, maxML := 0, 0, 0
	for _, s := range seqs {
		ll, _, _ := literalLengthCode(int(s.litLen))
		ml, _, _ := matchLengthCode(int(s.matchLen))
		of := bitLength(s.ofValue) - 1
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
		ctableLiteralLength, z.llNorm[:])
	ofT := fitTable(ofCounts[:], maxOF, len(seqs), maxOffsetLog,
		ctableOffset, z.ofNorm[:])
	mlT := fitTable(mlCounts[:], maxML, len(seqs), maxMatchLengthLog,
		ctableMatchLength, z.mlNorm[:])

	dst = append(dst, sequenceCountHeader(len(seqs))...)
	dst = append(dst, llT.mode<<6|ofT.mode<<4|mlT.mode<<2)
	// Descriptions follow the byte in literal-length, offset, match-length order.
	for _, t := range [3]seqTable{llT, ofT, mlT} {
		switch t.mode {
		case modeRLE:
			dst = append(dst, t.rle)
		case modeFSE:
			dst = appendTableDescription(dst, t.norm, t.tableLog)
		}
	}

	bw := bitWriter{out: dst}

	codes := func(s sequence) (llCode, llBits uint8, llExtra uint32,
		mlCode, mlBits uint8, mlExtra uint32,
		ofCode uint8, ofExtra uint32) {
		llCode, llBits, llExtra = literalLengthCode(int(s.litLen))
		mlCode, mlBits, mlExtra = matchLengthCode(int(s.matchLen))
		ofCode = bitLength(s.ofValue) - 1
		ofExtra = s.ofValue - 1<<ofCode
		return
	}

	last := seqs[len(seqs)-1]
	llCode, llBits, llExtra, mlCode, mlBits, mlExtra, ofCode, ofExtra := codes(last)

	var llState, ofState, mlState fseState
	llState.init(llT.ct, llCode)
	ofState.init(ofT.ct, ofCode)
	mlState.init(mlT.ct, mlCode)

	bw.addBits(llExtra, llBits)
	bw.addBits(mlExtra, mlBits)
	bw.flush32()
	bw.addBits(ofExtra, ofCode)

	for i := len(seqs) - 2; i >= 0; i-- {
		llCode, llBits, llExtra, mlCode, mlBits, mlExtra, ofCode, ofExtra = codes(seqs[i])

		bw.flush32()
		ofState.encode(&bw, ofCode)
		mlState.encode(&bw, mlCode)
		llState.encode(&bw, llCode)

		// The decoder reads literal length, then match length, then offset, so
		// the extra bits go out in that order: lowest bits first.
		extra := uint64(ofExtra)
		extra = extra<<mlBits | uint64(mlExtra)
		extra = extra<<llBits | uint64(llExtra)
		width := llBits + mlBits + ofCode

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
// the raw and RLE alternatives. The caller falls back when it did not.
func (z *Writer) appendCompressedBlock(dst []byte, p []byte, last bool) ([]byte, bool) {
	if len(z.seqs) == 0 {
		return dst, false
	}

	// Literals are every byte no match covered, in order.
	z.literals = z.literals[:0]
	at := 0
	for _, s := range z.seqs {
		z.literals = append(z.literals, p[at:at+int(s.litLen)]...)
		at += int(s.litLen) + int(s.matchLen)
	}
	z.literals = append(z.literals, p[at:]...)
	applyRepeatOffsets(z.seqs)

	start := len(dst)
	dst = append(dst, 0, 0, 0) // Block_Header, rewritten once the size is known
	dst = z.appendLiterals(dst)
	dst = z.appendSequences(dst)

	size := len(dst) - start - 3
	// rleBlockSize counts the block headers it would need, so this block's own
	// three must be counted too or a compressed block wins comparisons it loses.
	if size+3 >= rleBlockSize(p) || size >= 1<<21 {
		return dst[:start], false
	}
	header := uint32(size)<<3 | 2<<1 // Block_Type 2: compressed
	if last {
		header |= 1
	}
	dst[start] = byte(header)
	dst[start+1] = byte(header >> 8)
	dst[start+2] = byte(header >> 16)
	return dst, true
}
