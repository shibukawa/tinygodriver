//go:build force_tinygo_logic

// Round-trip coverage for the bounded encoder against the reference decoder.
//
// The table descriptions and the interleaved sequence bitstream are the kind of
// code where an off-by-one produces a frame that looks plausible and decodes to
// nothing, so the only test that means anything is whether a real decoder agrees.
// These cases exist to reach the corners the ratio tests do not: sizes either
// side of the block and sequence limits, alphabets narrow enough to force RLE
// tables and wide enough to force the largest accuracy, and content with no
// matches at all.

package zstd

import (
	"bytes"
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

func TestRoundTripShapes(t *testing.T) {
	rnd := rand.New(rand.NewSource(20260808))

	randomBytes := func(n int) []byte {
		b := make([]byte, n)
		rnd.Read(b)
		return b
	}
	// alphabet builds content drawn from a limited symbol set, which is what
	// pushes the fitted tables toward few symbols and low accuracy.
	alphabet := func(n, symbols int) []byte {
		b := make([]byte, n)
		for i := range b {
			b[i] = byte('a' + rnd.Intn(symbols))
		}
		return b
	}
	structured := func(lines int) []byte {
		var b bytes.Buffer
		for i := range lines {
			fmt.Fprintf(&b, "row %d: name=item-%d value=%d\n", i, i, i*7%1000)
		}
		return b.Bytes()
	}

	cases := []struct {
		name string
		data []byte
	}{
		{"empty", nil},
		{"one byte", []byte("x")},
		{"two bytes", []byte("xy")},
		{"three bytes", []byte("xyz")},
		{"shorter than min match", []byte("ab")},
		{"exactly min match", []byte("abcd")},
		{"single run", bytes.Repeat([]byte{'q'}, 5000)},
		{"two symbols", alphabet(4000, 2)},
		{"whole alphabet", alphabet(20000, 26)},
		{"random 1 byte", randomBytes(1)},
		{"random 100", randomBytes(100)},
		{"random 70000", randomBytes(70000)},
		{"structured 10", structured(10)},
		{"structured 2000", structured(2000)},

		// Either side of the sequence cap, which ends a block early.
		{"structured 8000", structured(8000)},

		// Either side of the 128 KiB block boundary.
		{"just under a block", structured(1)[:1]},
		{"one block", bytes.Repeat(structured(400), 1)},
		{"over a block", bytes.Repeat(structured(400), 20)},

		// Literal counts either side of the header size boundaries at 31 and 4095.
		{"31 literals", randomBytes(31)},
		{"32 literals", randomBytes(32)},
		{"4095 literals", randomBytes(4095)},
		{"4096 literals", randomBytes(4096)},

		{"matches then noise", append(bytes.Repeat([]byte("abcabcabc"), 200), randomBytes(3000)...)},
		{"noise then matches", append(randomBytes(3000), bytes.Repeat([]byte("abcabcabc"), 200)...)},
		{"long offsets", append(randomBytes(60000), []byte("abcdefghij")...)},
	}

	for _, c := range cases {
		encoded, result, err := EncodeAll(c.data, WithETag(false))
		if err != nil {
			t.Errorf("%s: EncodeAll: %v", c.name, err)
			continue
		}
		if int(result.Size) != len(encoded) {
			t.Errorf("%s: Result.Size = %d, encoded %d bytes", c.name, result.Size, len(encoded))
		}
		assertReferenceDecode(t, encoded, c.data)
	}
}

// TestRoundTripStreaming drives the same ground through Write and Flush, where a
// block boundary can land anywhere and Flush can cut one short.
func TestRoundTripStreaming(t *testing.T) {
	source := []byte(strings.Repeat(
		"GET /items/42 HTTP/1.1\r\nHost: example.com\r\nAccept: */*\r\n\r\n", 2000))

	for _, chunk := range []int{1, 7, 64, 999, 5000, 70000} {
		for _, flushEvery := range []int{0, 1, 5} {
			var out bytes.Buffer
			z, err := NewWriter(&out, WithETag(false))
			if err != nil {
				t.Fatalf("NewWriter: %v", err)
			}
			written := 0
			for i := 0; written < len(source); i++ {
				n := chunk
				if written+n > len(source) {
					n = len(source) - written
				}
				if _, err := z.Write(source[written : written+n]); err != nil {
					t.Errorf("chunk=%d flush=%d: Write: %v", chunk, flushEvery, err)
					break
				}
				written += n
				if flushEvery != 0 && i%flushEvery == 0 {
					if err := z.Flush(); err != nil {
						t.Errorf("chunk=%d flush=%d: Flush: %v", chunk, flushEvery, err)
						break
					}
				}
			}
			if err := z.Close(); err != nil {
				t.Errorf("chunk=%d flush=%d: Close: %v", chunk, flushEvery, err)
				continue
			}
			assertReferenceDecode(t, out.Bytes(), source)
		}
	}
}

// TestNormalizeCountsSumsToTableSize checks the invariant the format depends on:
// the distribution must account for exactly every state, or the decoder builds a
// different table than the encoder used.
func TestNormalizeCountsSumsToTableSize(t *testing.T) {
	rnd := rand.New(rand.NewSource(5))
	for range 2000 {
		symbols := 1 + rnd.Intn(52)
		counts := make([]uint32, symbols)
		total := 0
		used := 0
		for i := range counts {
			if rnd.Intn(3) == 0 {
				continue
			}
			counts[i] = uint32(1 + rnd.Intn(5000))
			total += int(counts[i])
			used++
		}
		if used == 0 {
			continue
		}
		for tableLog := uint8(minTableLog); tableLog <= 9; tableLog++ {
			if 1<<tableLog < used {
				continue
			}
			norm := make([]int16, symbols)
			if !normalizeCounts(counts, norm, total, tableLog) {
				continue
			}
			sum := 0
			for i, v := range norm {
				if v < 0 {
					t.Fatalf("norm[%d] = %d, negative values are not produced", i, v)
				}
				if (counts[i] == 0) != (v == 0) {
					t.Fatalf("norm[%d] = %d for count %d: used and unused must not swap",
						i, v, counts[i])
				}
				sum += int(v)
			}
			if sum != 1<<tableLog {
				t.Fatalf("tableLog %d: norm sums to %d, want %d", tableLog, sum, 1<<tableLog)
			}
		}
	}
}
