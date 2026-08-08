//go:build force_tinygo_logic

// Compression-ratio coverage for the bounded encoder, on the payload shapes a
// web server actually sends. These run only under force_tinygo_logic, because
// that is what selects the bounded encoder on host Go; a normal host build tests
// klauspost instead, whose ratios are not this package's business.
//
// Every case decodes through the reference implementation, so a ratio can never
// improve by emitting something a decoder would reject.

package zstd

import (
	"bytes"
	"compress/flate"
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

func ratioPayloads() []struct {
	name string
	data []byte
} {
	var html bytes.Buffer
	html.WriteString("<!doctype html><html><head><title>Index</title></head><body>\n")
	for i := range 200 {
		fmt.Fprintf(&html, "  <li class=%q data-id=%q><a href=\"/items/%d\">Item %d</a></li>\n",
			"item", fmt.Sprint(i), i, i)
	}
	html.WriteString("</body></html>\n")

	var js bytes.Buffer
	js.WriteString(`{"items":[`)
	for i := range 200 {
		if i > 0 {
			js.WriteByte(',')
		}
		fmt.Fprintf(&js, `{"id":%d,"name":"item-%d","active":true,"score":%d.5}`, i, i, i%97)
	}
	js.WriteString(`]}`)

	// Varied text, deliberately not a repeated block: a repeated one would be a
	// run-length test wearing prose as a disguise.
	var prose bytes.Buffer
	words := strings.Fields("the quick brown fox jumps over a lazy dog while " +
		"packing my box with five dozen liquor jugs before dawn broke over " +
		"quiet harbours where fishermen mended nets and counted their catch")
	pr := rand.New(rand.NewSource(7))
	for range 900 {
		prose.WriteString(words[pr.Intn(len(words))])
		prose.WriteByte(' ')
	}

	random := make([]byte, 8192)
	rand.New(rand.NewSource(1)).Read(random)

	return []struct {
		name string
		data []byte
	}{
		{"html listing", html.Bytes()},
		{"json array", js.Bytes()},
		{"varied text", prose.Bytes()},
		{"repeated string", []byte(strings.Repeat("compressme", 200))},
		{"incompressible", random},
	}
}

func deflateSize(t *testing.T, src []byte) int {
	t.Helper()
	var b bytes.Buffer
	w, err := flate.NewWriter(&b, 6)
	if err != nil {
		t.Fatalf("flate.NewWriter: %v", err)
	}
	if _, err := w.Write(src); err != nil {
		t.Fatalf("flate write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("flate close: %v", err)
	}
	return b.Len()
}

// TestRatioAgainstDeflate is the regression guard for the whole point of the
// sequence encoder. Before it emitted more than one match per block, the HTML
// and JSON cases came out at 99.9% of their input, which is why these bounds are
// expressed against deflate rather than as absolute sizes: deflate is the
// encoding a server would otherwise have negotiated, so losing badly to it means
// the zstd frame is not worth sending.
func TestRatioAgainstDeflate(t *testing.T) {
	// Ratio of encoded size to deflate's, above which the case fails. Text with
	// few repeats is where a literal coder would earn its keep, so it is allowed
	// more room than the structured payloads.
	limits := map[string]float64{
		"html listing":    0.95,
		"json array":      1.00,
		"varied text":     1.22,
		"repeated string": 1.00,
		"incompressible":  1.05,
	}

	for _, p := range ratioPayloads() {
		encoded, _, err := EncodeAll(p.data, WithETag(false))
		if err != nil {
			t.Errorf("%s: EncodeAll: %v", p.name, err)
			continue
		}
		assertReferenceDecode(t, encoded, p.data)

		fl := deflateSize(t, p.data)
		ratio := float64(len(encoded)) / float64(fl)
		t.Logf("%-16s raw %6d  zstd %6d (%5.1f%%)  deflate %6d (%5.1f%%)  zstd/deflate %.2fx",
			p.name, len(p.data),
			len(encoded), 100*float64(len(encoded))/float64(len(p.data)),
			fl, 100*float64(fl)/float64(len(p.data)), ratio)

		if limit, ok := limits[p.name]; ok && ratio > limit {
			t.Errorf("%s: %d bytes is %.2fx deflate's %d, above the %.2fx limit",
				p.name, len(encoded), ratio, fl, limit)
		}
	}
}

// TestSectionBreakdown reports where a block's bytes go, which is what tells you
// whether the next improvement belongs in the literal coder or the match finder.
// It asserts nothing; it exists so the answer is one test run away.
func TestSectionBreakdown(t *testing.T) {
	for _, p := range ratioPayloads() {
		z, err := NewWriter(&bytes.Buffer{}, WithETag(false))
		if err != nil {
			t.Fatalf("NewWriter: %v", err)
		}

		consumed := z.findSequences(p.data)
		seqs := len(z.seqs)
		var matched, lits int
		for _, s := range z.seqs {
			matched += int(s.matchLen)
			lits += int(s.litLen)
		}
		lits += consumed - matched - lits

		encoded, _, err := EncodeAll(p.data, WithETag(false))
		if err != nil {
			t.Fatalf("%s: EncodeAll: %v", p.name, err)
		}
		seqBytes := len(encoded) - lits
		t.Logf("%-16s raw %6d  encoded %6d  seqs %5d  matched %5.1f%%  literals %5d  sequences+headers ~%5d",
			p.name, len(p.data), len(encoded), seqs,
			100*float64(matched)/float64(len(p.data)), lits, seqBytes)
	}
}
