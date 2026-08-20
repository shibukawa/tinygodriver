//go:build tinygo || force_tinygo_logic

// Per-block FSE tables for the sequences section.
//
// The predefined distributions cost nothing to transmit but charge about twenty
// bits a sequence on structured content, because a real block's symbols are far
// more skewed than the defaults: match lengths cluster, and a document's offsets
// are a handful of values repeated. Fitting the tables to the block and paying
// twenty or thirty bytes to describe them is what brings the cost down to what a
// fitted Huffman tree gets in deflate.
//
// A table is only transmitted when the block has enough sequences to pay for the
// description; below that, and whenever the fitted table would not actually be
// smaller, the predefined one is used instead.

package zstd

// Symbol_Compression_Mode values.
const (
	modePredefined = 0
	modeRLE        = 1
	modeFSE        = 2
)

// Accuracy limits the format places on each sequence table.
const (
	maxLiteralLengthLog = 9
	maxMatchLengthLog   = 9
	maxOffsetLog        = 8
	minTableLog         = 5
)

// seqTable is one of the three tables a block's sequences are coded against.
type seqTable struct {
	mode byte
	ct   *ctable // for modePredefined and modeFSE
	rle  byte    // for modeRLE

	norm     []int16 // for modeFSE, the fitted distribution
	tableLog uint8
}

// fitTable chooses how to code one symbol stream. counts is indexed by symbol,
// maxSymbol is the largest with a nonzero count, and predef is the fallback.
//
// scratch and ctScratch are reused so that fitting three tables per block
// allocates nothing.
func fitTable(counts []uint32, maxSymbol int, nbSeq int, maxLog uint8,
	predef *ctable, scratch []int16, ctScratch *ctableScratch) seqTable {
	fallback := seqTable{mode: modePredefined, ct: predef}

	used := 0
	single := -1
	for s := 0; s <= maxSymbol; s++ {
		if counts[s] != 0 {
			used++
			single = s
		}
	}
	switch {
	case used == 0:
		return fallback
	case used == 1:
		// One symbol for the whole block: the description is the symbol itself,
		// and a one-state table emits no bits for it at all.
		return seqTable{mode: modeRLE, rle: byte(single), ct: ctScratch.rle(single)}
	}

	// A description runs to a few dozen bytes, so it needs a block with enough
	// sequences to spread that over. Below this the predefined table wins even
	// though it codes each sequence less well.
	const minSeqForOwnTable = 48
	if nbSeq < minSeqForOwnTable {
		return fallback
	}

	// Accuracy: enough states to give every used symbol at least one, and no
	// more than the sequence count can justify -- a table finer than the data is
	// description bytes spent on noise.
	tableLog := uint8(minTableLog)
	for tableLog < maxLog && 1<<tableLog < used {
		tableLog++
	}
	for tableLog < maxLog && 1<<(tableLog+1) <= nbSeq {
		tableLog++
	}
	if 1<<tableLog < used {
		return fallback // cannot seat every symbol; the format forbids it
	}

	norm := scratch[:maxSymbol+1]
	if !normalizeCounts(counts[:maxSymbol+1], norm, nbSeq, tableLog) {
		return fallback
	}
	return seqTable{
		mode:     modeFSE,
		ct:       ctScratch.fitted(norm, tableLog),
		norm:     norm,
		tableLog: tableLog,
	}
}

// normalizeCounts scales counts into norm so that the entries sum to exactly
// 1<<tableLog, every used symbol keeps at least one state, and unused symbols
// get none. It reports false when that is not possible.
//
// Low-probability symbols could be given the format's -1, which buys a symbol
// less than one state's worth of probability. One state is used instead: it
// costs a fraction of a bit per occurrence and removes a whole case from both
// this function and the description writer.
func normalizeCounts(counts []uint32, norm []int16, total int, tableLog uint8) bool {
	tableSize := 1 << tableLog
	sum := 0
	largest, largestCount := -1, uint32(0)
	for s, c := range counts {
		if c == 0 {
			norm[s] = 0
			continue
		}
		share := int(uint64(c) << tableLog / uint64(total))
		if share < 1 {
			share = 1
		}
		norm[s] = int16(share)
		sum += share
		if c > largestCount {
			largestCount, largest = c, s
		}
	}
	if largest < 0 {
		return false
	}

	// Settle the difference on the most frequent symbol, which is the one whose
	// probability a small change disturbs least.
	if sum < tableSize {
		norm[largest] += int16(tableSize - sum)
		return true
	}
	for sum > tableSize {
		over := sum - tableSize
		if int(norm[largest]) > over {
			norm[largest] -= int16(over)
			return true
		}
		// The largest cannot absorb it alone; shave every symbol that can spare a
		// state and settle the rest on the next pass.
		progress := false
		for s := range norm {
			if sum == tableSize {
				break
			}
			if norm[s] > 1 {
				norm[s]--
				sum--
				progress = true
			}
		}
		if !progress {
			return false
		}
	}
	return true
}

// appendTableDescription writes a fitted distribution.
//
// The encoding is a bitstream, least-significant bit first, of one value per
// symbol: the probability plus one, in a width that narrows as the states run
// out. A zero probability is followed by a count of how many more symbols are
// also zero, in two-bit groups, which is what keeps a mostly-empty alphabet
// cheap. The stream ends as soon as the states are accounted for, so the symbols
// after the last used one need not be written at all.
func appendTableDescription(dst []byte, norm []int16, tableLog uint8) []byte {
	// A description is a few dozen bits; the writer is a plain struct rather
	// than a closure over its three locals, which would box them all.
	dw := descWriter{dst: dst}

	dw.put(uint32(tableLog-minTableLog), 4)

	tableSize := int32(1) << tableLog
	remaining := tableSize + 1 // the extra state is the format's own accounting
	threshold := tableSize
	nbBits := uint(tableLog + 1)

	previous0 := false
	symbol := 0
	for remaining > 1 && symbol < len(norm) {
		if previous0 {
			// Count the zero run that follows, in groups of three, with 24 at a
			// time for long stretches.
			start := symbol
			for symbol < len(norm) && norm[symbol] == 0 {
				symbol++
			}
			for symbol >= start+24 {
				start += 24
				dw.put(0xFFFF, 16)
			}
			for symbol >= start+3 {
				start += 3
				dw.put(3, 2)
			}
			dw.put(uint32(symbol-start), 2)
			if symbol >= len(norm) {
				break
			}
		}

		count := norm[symbol]
		symbol++

		max := (2*threshold - 1) - remaining
		remaining -= int32(count)

		v := uint32(count) + 1 // the value on the wire is one higher
		if int32(v) >= threshold {
			dw.put(v+uint32(max), nbBits)
		} else if int32(v) < max {
			dw.put(v, nbBits-1)
		} else {
			dw.put(v, nbBits)
		}

		previous0 = count == 0
		for remaining < threshold {
			nbBits--
			threshold >>= 1
		}
	}

	// Flush whatever is left, padding to a byte.
	for n := (dw.bitCount + 7) / 8; n > 0; n-- {
		dw.dst = append(dw.dst, byte(dw.bitStream))
		dw.bitStream >>= 8
	}
	return dw.dst
}

// descWriter accumulates a table description's bitstream, least-significant
// bit first, sixteen bits at a time.
type descWriter struct {
	bitStream uint32
	bitCount  uint
	dst       []byte
}

func (w *descWriter) put(v uint32, n uint) {
	w.bitStream |= v << w.bitCount
	w.bitCount += n
	if w.bitCount >= 16 {
		w.dst = append(w.dst, byte(w.bitStream), byte(w.bitStream>>8))
		w.bitStream >>= 16
		w.bitCount -= 16
	}
}
