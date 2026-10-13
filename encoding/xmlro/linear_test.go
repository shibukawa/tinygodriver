package xmlro

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"
)

// unescapeBySearch is Unescape as it was first written: correct, and the
// statement of what Unescape returns, but it searches the rest of src for a
// '\r' and a ';' at every '&', and for an '&' at every '\r', which is the
// square of the length when the one searched for is not there.
func unescapeBySearch(dst, src []byte) []byte {
	for {
		i := bytes.IndexByte(src, '&')
		j := bytes.IndexByte(src, '\r')
		if i < 0 && j < 0 {
			return append(dst, src...)
		}
		if i < 0 || (j >= 0 && j < i) {
			dst = append(dst, src[:j]...)
			dst = append(dst, '\n')
			src = src[j+1:]
			if len(src) > 0 && src[0] == '\n' {
				src = src[1:]
			}
			continue
		}
		dst = append(dst, src[:i]...)
		src = src[i:]
		j = bytes.IndexByte(src, ';')
		if j < 0 || j > maxEntityName {
			dst = append(dst, '&')
			src = src[1:]
			continue
		}
		var ok bool
		dst, ok = appendReference(dst, src[1:j])
		if !ok {
			dst = append(dst, '&')
			src = src[1:]
			continue
		}
		src = src[j+1:]
	}
}

// Every string of up to six of the bytes decoding looks at, and a few it
// does not, decodes as the searching version decodes it.
func TestUnescapeMatchesSearch(t *testing.T) {
	const alphabet = "&;\r\n#x4lt"
	var in []byte
	var walk func(depth int)
	walk = func(depth int) {
		if got, want := Unescape(nil, in), unescapeBySearch(nil, in); !bytes.Equal(got, want) {
			t.Fatalf("Unescape(%q) = %q, want %q", in, got, want)
		}
		if depth == 0 {
			return
		}
		for i := range len(alphabet) {
			in = append(in, alphabet[i])
			walk(depth - 1)
			in = in[:len(in)-1]
		}
	}
	walk(6)
}

// The ';' of a reference is looked for within maxEntityName bytes of its
// '&', and a reference may be exactly that long.
func TestUnescapeNameBound(t *testing.T) {
	for pad := maxEntityName - 8; pad <= maxEntityName+8; pad++ {
		// "&#0...065;" with the ';' at offset pad+4.
		in := []byte("x&#" + strings.Repeat("0", pad) + "65;y&amp;\rz")
		want := "xAy&\nz"
		if pad+4 > maxEntityName {
			want = "x" + string(in[1:len(in)-8]) + "y&\nz"
		}
		if got := string(Unescape(nil, in)); got != want {
			t.Errorf("';' at %d: Unescape = %q, want %q", pad+4, got, want)
		}
		if got, want := Unescape(nil, in), unescapeBySearch(nil, in); !bytes.Equal(got, want) {
			t.Errorf("';' at %d: Unescape = %q, the searching version %q", pad+4, got, want)
		}
	}
}

// linear fails when work(8n) takes more than 32 times as long as work(n):
// 8 times is linear and 64 is the square. Each is the best of three runs,
// and a larger run too short to measure passes.
func linear(t *testing.T, name string, n int, work func(n int) func()) {
	t.Helper()
	best := func(n int) time.Duration {
		run := work(n)
		d := time.Duration(1<<63 - 1)
		for range 3 {
			start := time.Now()
			run()
			d = min(d, time.Since(start))
		}
		return d
	}
	small, large := best(n), best(8*n)
	if large > 50*time.Millisecond && large > 32*small {
		t.Errorf("%s: %v for %d, %v for %d: not linear", name, small, n, large, 8*n)
	}
}

// No text makes decoding it cost more than its length: not ampersands with
// no ';' or '\r' after them, not carriage returns with no '&', not
// references, complete or not.
func TestUnescapeIsLinear(t *testing.T) {
	for _, unit := range []string{"&", "a&b ", "\r", "\r\n", "&amp;", "&#;", "&#65;", "&;", "&\r", "&" + strings.Repeat("a", maxEntityName)} {
		linear(t, fmt.Sprintf("Unescape of %q repeated", unit), (64<<10)/len(unit)+1, func(n int) func() {
			src := bytes.Repeat([]byte(unit), n)
			dst := make([]byte, 0, len(src))
			return func() { Unescape(dst, src) }
		})
	}
}

// entityDocument declares n entities and refers to each once.
func entityDocument(n int) []byte {
	var b bytes.Buffer
	b.WriteString("<!DOCTYPE r [")
	for i := range n {
		fmt.Fprintf(&b, "<!ENTITY e%d \"v%d\">", i, i)
	}
	b.WriteString("]><r>")
	for i := range n {
		fmt.Fprintf(&b, "&e%d;", i)
	}
	b.WriteString("</r>")
	return b.Bytes()
}

// A document that declares many entities and refers to them often does not
// pay for one comparison per declaration at every reference.
func TestEntityLookupIsLinear(t *testing.T) {
	linear(t, "declared entities, each referred to once", 4000, func(n int) func() {
		doc := entityDocument(n)
		r := NewBytesReader(nil, Options{AllowDoctype: true, MaxBufferBytes: 1 << 30})
		return func() {
			r.ResetBytes(doc)
			for {
				if k, err := r.Next(); err != nil {
					t.Fatal(err)
				} else if k == EOF {
					return
				}
			}
		}
	})
}

// The declarations are searched in sorted order; the first of a name still
// wins, a name not declared still goes to Options.Entities, and one neither
// knows is left as written.
func TestEntityLookupAmongMany(t *testing.T) {
	const names, decls = 37, 200
	var b strings.Builder
	b.WriteString("<!DOCTYPE r [")
	for i := range decls {
		// Names in an order that is not the sorted one, each declared
		// several times.
		fmt.Fprintf(&b, "<!ENTITY e%d \"v%d;\">", (i*7)%names, i)
	}
	b.WriteString("]><r>")
	var want strings.Builder
	first := map[int]int{}
	for i := range decls {
		if _, ok := first[(i*7)%names]; !ok {
			first[(i*7)%names] = i
		}
	}
	for k := range names {
		fmt.Fprintf(&b, "&e%d;", k)
		fmt.Fprintf(&want, "v%d;", first[k])
	}
	b.WriteString("&e;&e370;&table;&unknown;</r>")
	want.WriteString("&e;&e370;from the table&unknown;")

	r := NewBytesReader([]byte(b.String()), Options{AllowDoctype: true, Entities: func(name []byte) (string, bool) {
		return "from the table", Equal(name, "table")
	}})
	for {
		k, err := r.Next()
		if err != nil {
			t.Fatal(err)
		}
		if k == Text {
			if got := r.Text().String(); got != want.String() {
				t.Errorf("text = %q, want %q", got, want.String())
			}
			return
		}
	}
}

// sortEntities leaves the declarations in order of name, those of one name
// in the order they were declared, for every count and arrangement: the
// first entry of a name is then the declaration that wins.
func TestSortEntities(t *testing.T) {
	seed := uint32(1)
	next := func(n int) int {
		seed = seed*1664525 + 1013904223
		return int(seed>>16) % n
	}
	for count := range 130 {
		var r Reader
		first := map[string]int{}
		for i := range count {
			name := fmt.Sprintf("e%d", next(count/2+1))
			if _, ok := first[name]; !ok {
				first[name] = i
			}
			r.docEnts = append(r.docEnts, docEntity{name: name, value: fmt.Sprint(i), order: i})
		}
		r.sortEntities()
		for i := 1; i < count; i++ {
			if !r.docEnts[i-1].before(&r.docEnts[i]) {
				t.Fatalf("%d declarations: %v is not before %v", count, r.docEnts[i-1], r.docEnts[i])
			}
		}
		for name, i := range first {
			if v, ok := r.lookupEntity([]byte(name)); !ok || v != fmt.Sprint(i) {
				t.Fatalf("%d declarations: %s is %q, %v; declared first as %d", count, name, v, ok, i)
			}
		}
		if _, ok := r.lookupEntity([]byte("e")); ok {
			t.Fatalf("%d declarations: found a name not declared", count)
		}
	}
}
