//go:build force_tinygo_logic

// Coverage for the literal coder's own decision points, which the payload-shaped
// tests reach only by accident: the four-stream layout, the symbol ceiling the
// direct weight representation imposes, the depth limit, and each of the three
// literals block types.

package zstd

import (
	"bytes"
	"math/rand"
	"testing"
)

// litsFor returns the literals a block of src would carry, which is what the
// coder actually sees.
func litsFor(t *testing.T, src []byte) *Writer {
	t.Helper()
	z, err := NewWriter(&bytes.Buffer{}, WithETag(false))
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	consumed := z.findSequences(src)
	z.literals = z.literals[:0]
	at := 0
	for _, s := range z.seqs {
		z.literals = append(z.literals, src[at:at+int(s.litLen)]...)
		at += int(s.litLen) + int(s.matchLen)
	}
	z.literals = append(z.literals, src[at:consumed]...)
	return z
}

// literalsBlockType reads back the type the coder chose.
func literalsBlockType(section []byte) int {
	if len(section) == 0 {
		return -1
	}
	return int(section[0] & 3)
}

func TestLiteralsBlockTypes(t *testing.T) {
	cases := []struct {
		name string
		lits []byte
		want int
	}{
		{"empty", nil, literalsRaw},
		{"one distinct byte", bytes.Repeat([]byte{'z'}, 900), literalsRLE},
		{"too few to code", []byte("abcabcabcabcabc"), literalsRaw},
		{"worth coding", bytes.Repeat([]byte("abcdefabc"), 40), literalsCompressed},
	}
	for _, c := range cases {
		z, err := NewWriter(&bytes.Buffer{}, WithETag(false))
		if err != nil {
			t.Fatalf("NewWriter: %v", err)
		}
		z.literals = append(z.literals[:0], c.lits...)
		got := literalsBlockType(z.appendLiterals(nil))
		if got != c.want {
			t.Errorf("%s: literals block type = %d, want %d", c.name, got, c.want)
		}
	}
}

// TestLiteralsSymbolCeiling pins the fallback that keeps the direct weight
// representation honest. Its header byte is 127 plus the number of weights, so a
// literal byte above 128 cannot be described and the section must store instead
// of producing a table the decoder would reject.
func TestLiteralsSymbolCeiling(t *testing.T) {
	build := func(top byte) []byte {
		lits := make([]byte, 0, 600)
		for i := range 600 {
			if i%37 == 0 {
				lits = append(lits, top)
				continue
			}
			lits = append(lits, byte('a'+i%20))
		}
		return lits
	}

	for _, tc := range []struct {
		name string
		top  byte
		want int
	}{
		{"largest symbol 128", 128, literalsCompressed},
		{"largest symbol 129", 129, literalsRaw},
		{"largest symbol 255", 255, literalsRaw},
	} {
		z, err := NewWriter(&bytes.Buffer{}, WithETag(false))
		if err != nil {
			t.Fatalf("NewWriter: %v", err)
		}
		z.literals = append(z.literals[:0], build(tc.top)...)
		if got := literalsBlockType(z.appendLiterals(nil)); got != tc.want {
			t.Errorf("%s: literals block type = %d, want %d", tc.name, got, tc.want)
		}
	}
}

// TestHuffTableDepthWithinLimit checks the property the format requires of every
// code the coder emits, across skews severe enough that a plain Huffman tree
// would run deeper than eleven bits.
func TestHuffTableDepthWithinLimit(t *testing.T) {
	rnd := rand.New(rand.NewSource(11))
	for range 3000 {
		var counts [256]uint32
		total := 0
		used := 0
		// A geometric-ish skew: a few very common symbols and a long tail of ones
		// is exactly what pushes a tree deep.
		for s := range 1 + rnd.Intn(120) {
			c := uint32(1)
			if rnd.Intn(4) == 0 {
				c = uint32(1 + rnd.Intn(1<<uint(rnd.Intn(20))))
			}
			counts[s] = c
			total += int(c)
			used++
		}
		if used < 2 {
			continue
		}
		tbl, ok := buildHuffTable(&counts, total)
		if !ok {
			continue
		}
		if tbl.maxBit > maxHuffBits {
			t.Fatalf("longest code is %d bits, the format allows %d", tbl.maxBit, maxHuffBits)
		}

		// Every used symbol needs a code, and no unused symbol may have one.
		longest := 0
		for s := 0; s <= tbl.max; s++ {
			if (counts[s] == 0) != (tbl.bits[s] == 0) {
				t.Fatalf("symbol %d: count %d but %d bits", s, counts[s], tbl.bits[s])
			}
			if tbl.bits[s] == tbl.maxBit {
				longest++
			}
		}
		// The decoder rejects a table whose longest codes are odd in number or
		// fewer than two, which a complete binary tree never produces.
		if longest < 2 || longest%2 != 0 {
			t.Fatalf("%d symbols at the longest length, want an even number of at least 2", longest)
		}

		// Kraft equality: a complete code accounts for the whole space, which is
		// also what lets the decoder derive the last symbol's weight.
		sum := 0
		for s := 0; s <= tbl.max; s++ {
			if tbl.bits[s] != 0 {
				sum += 1 << (tbl.maxBit - tbl.bits[s])
			}
		}
		if sum != 1<<tbl.maxBit {
			t.Fatalf("code space sums to %d, want %d", sum, 1<<tbl.maxBit)
		}
	}
}

// TestLiteralsFourStreams drives the layout that takes over above the
// single-stream size limit, and checks the jump table describes what follows.
func TestLiteralsFourStreams(t *testing.T) {
	// Enough literals to cross fourStreamLiterals, drawn from a small alphabet so
	// they compress and the section stays under the 10-bit size fields.
	rnd := rand.New(rand.NewSource(3))
	lits := make([]byte, 4000)
	for i := range lits {
		lits[i] = byte('a' + rnd.Intn(6))
	}

	z, err := NewWriter(&bytes.Buffer{}, WithETag(false))
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	z.literals = append(z.literals[:0], lits...)
	section := z.appendLiterals(nil)
	if got := literalsBlockType(section); got != literalsCompressed {
		t.Fatalf("literals block type = %d, want compressed", got)
	}
	if format := (section[0] >> 2) & 3; format == 0 {
		t.Fatalf("size format = 0, which is the single-stream layout; %d literals need four", len(lits))
	}

	// The whole section has to round-trip, and only a frame can prove that.
	src := make([]byte, 0, len(lits)*2)
	for i := range lits {
		src = append(src, lits[i])
		if i%3 == 0 {
			src = append(src, 'q', 'q', 'q', 'q', 'q')
		}
	}
	encoded, _, err := EncodeAll(src, WithETag(false))
	if err != nil {
		t.Fatalf("EncodeAll: %v", err)
	}
	assertReferenceDecode(t, encoded, src)
}
