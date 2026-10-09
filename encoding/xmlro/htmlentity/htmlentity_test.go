package htmlentity

import (
	"runtime"
	"testing"
)

func TestLookupResolvesTheHTMLNames(t *testing.T) {
	cases := map[string]string{"nbsp": " ", "copy": "©", "eacute": "é", "AElig": "Æ", "zwnj": "‌", "thetasym": "ϑ"}
	for name, want := range cases {
		if got, ok := Lookup([]byte(name)); !ok || got != want {
			t.Errorf("%s: %q %v, want %q", name, got, ok, want)
		}
	}
	// The HTML 4 set includes lt, gt, amp and quot; the reader never asks
	// about those, since it decodes them before consulting a table.
	for _, name := range []string{"", "apos", "nope", "NBSP", "nbsp;", "a"} {
		if got, ok := Lookup([]byte(name)); ok {
			t.Errorf("%q resolved to %q", name, got)
		}
	}
	// The table is sorted, which the binary search relies on.
	for i := 1; i < len(table); i++ {
		if table[i-1].name >= table[i].name {
			t.Fatalf("table out of order at %d: %q then %q", i, table[i-1].name, table[i].name)
		}
	}
	if len(table) != 252 {
		t.Errorf("%d entries, want the 252 of encoding/xml.HTMLEntity", len(table))
	}
}

func TestAutoCloseNamesTheVoidElements(t *testing.T) {
	for _, name := range []string{"br", "BR", "Br", "img", "xhtml:br", "svg:IMG", "hr", "meta", "param", "basefont"} {
		if !AutoClose([]byte(name)) {
			t.Errorf("%s not auto-closed", name)
		}
	}
	for _, name := range []string{"", "b", "div", "p", "brr", "xhtml:div", "images", "a:b:br", ":br", "br:", ":"} {
		if AutoClose([]byte(name)) {
			t.Errorf("%s auto-closed", name)
		}
	}
}

// Both calls allocate nothing: a reader asks them per reference and per
// start tag.
func TestLookupsAllocateNothing(t *testing.T) {
	names := [][]byte{[]byte("nbsp"), []byte("eacute"), []byte("nope"), []byte("br"), []byte("xhtml:img"), []byte("div")}
	var hits int
	fn := func() {
		for _, n := range names {
			if _, ok := Lookup(n); ok {
				hits++
			}
			if AutoClose(n) {
				hits++
			}
		}
	}
	fn()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for range 100 {
		fn()
	}
	runtime.ReadMemStats(&after)
	if n := after.TotalAlloc - before.TotalAlloc; n != 0 {
		t.Errorf("%d bytes allocated over 100 rounds", n)
	}
	_ = hits
}
