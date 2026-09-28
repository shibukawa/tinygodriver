//go:build tinygo || force_tinygo_logic

// The sequences section, decoded and executed: each sequence copies literals,
// then a match from the output already produced.

package zstd

// Symbol_Compression_Mode 3 reuses the previous block's table for the same
// stream, whatever mode built it. The encoder never writes it.
const modeRepeat = 3

// Alphabet sizes, less one, of the three symbol streams.
const (
	maxLiteralLengthSymbol = 35
	maxMatchLengthSymbol   = 52
	maxOffsetSymbol        = 31
)

// decodeSequences decodes the sequences section in src and executes it against
// lits, appending the block's content to out. reach is how many bytes before
// the end of out a match may copy from as the block begins.
func (d *decoder) decodeSequences(out, src, lits []byte, reach int) ([]byte, error) {
	if len(src) == 0 {
		return out, errCorruptSequences
	}
	nbSeq := int(src[0])
	pos := 1
	switch {
	case nbSeq == 0:
		// The literals are the whole block, and nothing may follow the count.
		if len(src) != 1 || len(lits) > d.blockMax {
			return out, errCorruptSequences
		}
		return append(out, lits...), nil
	case nbSeq == 255:
		if len(src) < 3 {
			return out, errCorruptSequences
		}
		nbSeq, pos = int(src[1])+int(src[2])<<8+0x7f00, 3
	case nbSeq >= 128:
		if len(src) < 2 {
			return out, errCorruptSequences
		}
		nbSeq, pos = (nbSeq-128)<<8|int(src[1]), 2
	}

	if len(src) <= pos {
		return out, errCorruptSequences
	}
	modes := src[pos]
	pos++
	if modes&3 != 0 {
		return out, errCorruptSequences // reserved bits
	}
	// Descriptions follow in literal-length, offset, match-length order.
	n, err := d.readSeqTable(&d.llTable, modes>>6, src[pos:],
		maxLiteralLengthSymbol, maxLiteralLengthLog, predefLiteralLengthNorm[:], predefLiteralLengthLog)
	if err != nil {
		return out, err
	}
	pos += n
	n, err = d.readSeqTable(&d.ofTable, modes>>4&3, src[pos:],
		maxOffsetSymbol, maxOffsetLog, predefOffsetNorm[:], predefOffsetLog)
	if err != nil {
		return out, err
	}
	pos += n
	n, err = d.readSeqTable(&d.mlTable, modes>>2&3, src[pos:],
		maxMatchLengthSymbol, maxMatchLengthLog, predefMatchLengthNorm[:], predefMatchLengthLog)
	if err != nil {
		return out, err
	}
	pos += n
	d.seqValid = true

	var br backReader
	if err := br.init(src[pos:]); err != nil {
		return out, err
	}
	ll, of, ml := &d.llTable, &d.ofTable, &d.mlTable
	// The encoder flushes match length, offset, then literal length last, so
	// they come off the stream in the opposite order; the three take at most
	// 26 bits, well inside what init loaded.
	llState := br.bits(ll.tableLog)
	ofState := br.bits(of.tableLog)
	mlState := br.bits(ml.tableLog)

	base := len(out)
	litPos := 0
	for i := nbSeq; i > 0; i-- {
		llE, ofE, mlE := ll.entries[llState], of.entries[ofState], ml.entries[mlState]

		// Extra bits come off in offset, match length, literal length order:
		// the reverse of how appendSequences packed them. An offset takes up
		// to 31 bits and the two lengths up to 16 each, so the lengths share
		// a fill.
		br.fill()
		offset := uint32(1)<<ofE.symbol + br.bits(ofE.symbol)
		br.fill()
		matchLen := uint32(matchLengthBases[mlE.symbol]) + br.bits(matchLengthBits[mlE.symbol])
		litLen := uint32(literalLengthBases[llE.symbol]) + br.bits(literalLengthBits[llE.symbol])

		// The states advance for every sequence but the last, literal length
		// first, as appendSequences wrote them in reverse.
		if i > 1 {
			br.fill()
			llState = uint32(llE.newState) + br.bits(llE.nbBits)
			mlState = uint32(mlE.newState) + br.bits(mlE.nbBits)
			ofState = uint32(ofE.newState) + br.bits(ofE.nbBits)
		}

		offset, err = d.resolveOffset(offset, litLen)
		if err != nil {
			return out, err
		}

		// Both lengths are under 2^17, so they fit an int on every target.
		lit, match := int(litLen), int(matchLen)
		if lit > len(lits)-litPos || lit+match > d.blockMax-(len(out)-base) {
			return out, errCorruptSequences
		}
		out = append(out, lits[litPos:litPos+lit]...)
		litPos += lit
		// The match may reach back across earlier blocks of this frame, but
		// never past its start nor further than the window. The offset can
		// take all 32 bits, so it is compared before it becomes an int, which
		// on 32-bit targets would turn it negative.
		if uint64(offset) > uint64(reach+len(out)-base) || uint64(offset) > uint64(d.window) {
			return out, errCorruptOffset
		}
		out = appendMatch(out, int(offset), match)
	}

	// The stream must end exactly where the last sequence did.
	br.fill()
	if !br.finished() {
		return out, errCorruptBitstream
	}
	rest := lits[litPos:]
	if len(rest) > d.blockMax-(len(out)-base) {
		return out, errCorruptSequences
	}
	return append(out, rest...), nil
}

// readSeqTable sets up t, one of the three sequence tables, for this block's
// mode, reading any description from the front of src and returning the bytes
// it took.
func (d *decoder) readSeqTable(t *fseDecTable, mode byte, src []byte,
	maxSymbol int, maxLog uint8, predef []int16, predefLog uint8) (int, error) {
	switch mode {
	case modePredefined:
		return 0, t.build(predef, predefLog)
	case modeRLE:
		if len(src) == 0 || int(src[0]) > maxSymbol {
			return 0, errCorruptSequences
		}
		t.rle(src[0])
		return 1, nil
	case modeFSE:
		norm := d.norm[:maxSymbol+1]
		n, tableLog, symbols, err := readFSEDescription(src, norm, maxLog)
		if err != nil {
			return 0, err
		}
		return n, t.build(norm[:symbols], tableLog)
	default: // modeRepeat
		if !d.seqValid {
			return 0, errCorruptSequences
		}
		return 0, nil
	}
}

// resolveOffset turns an Offset_Value into a distance, maintaining the three
// repeat slots.
//
// Values above 3 are explicit distances, stored three higher, and push the
// slots down. Values 1 to 3 name a slot -- shifted by one when the sequence has
// no literals, because repeating the previous distance straight after a match
// would only extend that match, and the fourth meaning this frees is slot one
// less one. A slot other than the first that gets used moves to the front.
func (d *decoder) resolveOffset(value, litLen uint32) (uint32, error) {
	if value > 3 {
		d.rep[2], d.rep[1], d.rep[0] = d.rep[1], d.rep[0], value-3
		return value - 3, nil
	}
	slot := value - 1
	if litLen == 0 {
		slot++
	}
	var dist uint32
	switch slot {
	case 0:
		return d.rep[0], nil
	case 1:
		dist = d.rep[1]
		d.rep[1] = d.rep[0]
	case 2:
		dist = d.rep[2]
		d.rep[2], d.rep[1] = d.rep[1], d.rep[0]
	default:
		dist = d.rep[0] - 1
		if dist == 0 {
			return 0, errCorruptOffset
		}
		d.rep[2], d.rep[1] = d.rep[1], d.rep[0]
	}
	d.rep[0] = dist
	return dist, nil
}

// appendMatch copies length bytes starting offset bytes back from the end of
// out. When the match overlaps its own output, the source region is periodic
// with period offset from where it starts, so doubling the copied span each
// round reproduces the byte-at-a-time result in a logarithmic number of copies.
func appendMatch(out []byte, offset, length int) []byte {
	start := len(out) - offset
	if length <= offset {
		return append(out, out[start:start+length]...)
	}
	for length > 0 {
		n := min(length, len(out)-start)
		out = append(out, out[start:start+n]...)
		length -= n
	}
	return out
}
