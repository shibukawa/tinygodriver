package xml

import (
	"bytes"
	"testing"
	"unsafe"
)

var sinkString string
var sinkInt int

// Every start tag's name converted to a string, the way a Token-returning
// parser must hand it over.
func BenchmarkNames_StringPerTag(b *testing.B) {
	b.SetBytes(int64(len(benchSheet)))
	b.ReportAllocs()
	src := bytes.NewReader(benchSheet)
	r := NewReader(src, Options{})
	for range b.N {
		src.Reset(benchSheet)
		r.Reset(src)
		for k := range r.Tokens() {
			if k == StartElement {
				sinkString = string(r.Name())
				if v, ok := r.Attr("r"); ok {
					sinkString = string(v)
				}
			}
		}
	}
}

// The same names compared as bytes against literals, which is what a
// consumer of this package writes.
func BenchmarkNames_BytesCompare(b *testing.B) {
	b.SetBytes(int64(len(benchSheet)))
	b.ReportAllocs()
	src := bytes.NewReader(benchSheet)
	r := NewReader(src, Options{})
	for range b.N {
		src.Reset(benchSheet)
		r.Reset(src)
		n := 0
		for k := range r.Tokens() {
			if k == StartElement {
				switch string(r.Name()) {
				case "c":
					n++
				case "row":
					n += 2
				case "v", "f":
					n += 3
				}
				if v, ok := r.Attr("r"); ok {
					n += len(v)
				}
			}
		}
		sinkInt = n
	}
}

// Interning: a string per distinct name, looked up alloc-free by the
// map[string] index conversion. What a consumer that wants strings for names
// would do; the attribute value is still copied because values are not a
// vocabulary.
func BenchmarkNames_Interned(b *testing.B) {
	b.SetBytes(int64(len(benchSheet)))
	b.ReportAllocs()
	src := bytes.NewReader(benchSheet)
	r := NewReader(src, Options{})
	intern := map[string]string{}
	for range b.N {
		src.Reset(benchSheet)
		r.Reset(src)
		for k := range r.Tokens() {
			if k == StartElement {
				name := r.Name()
				s, ok := intern[string(name)]
				if !ok {
					s = string(name)
					intern[s] = s
				}
				sinkString = s
				if v, ok := r.Attr("r"); ok {
					sinkInt += len(v)
				}
			}
		}
	}
}

// A zero-copy string view, unsafe.String over the buffer, valid until the
// next advance like the slice it views.
func BenchmarkNames_UnsafeStringView(b *testing.B) {
	b.SetBytes(int64(len(benchSheet)))
	b.ReportAllocs()
	src := bytes.NewReader(benchSheet)
	r := NewReader(src, Options{})
	for range b.N {
		src.Reset(benchSheet)
		r.Reset(src)
		n := 0
		for k := range r.Tokens() {
			if k == StartElement {
				name := r.Name()
				s := unsafe.String(unsafe.SliceData(name), len(name))
				switch s {
				case "c":
					n++
				case "row":
					n += 2
				case "v", "f":
					n += 3
				}
			}
		}
		sinkInt = n
	}
}

// Dispatch on the hash the reader already computed for nesting checks,
// against precomputed constants, instead of comparing bytes.
func fnv(s string) uint32 {
	h := uint32(fnvOffset)
	for i := 0; i < len(s); i++ {
		h = (h ^ uint32(s[i])) * fnvPrime
	}
	return h
}

func BenchmarkNames_HashSwitch(b *testing.B) {
	hc, hrow, hv, hf := fnv("c"), fnv("row"), fnv("v"), fnv("f")
	b.SetBytes(int64(len(benchSheet)))
	b.ReportAllocs()
	src := bytes.NewReader(benchSheet)
	r := NewReader(src, Options{})
	for range b.N {
		src.Reset(benchSheet)
		r.Reset(src)
		n := 0
		for k := range r.Tokens() {
			if k == StartElement {
				switch r.stack[r.depth-1] {
				case hc:
					n++
				case hrow:
					n += 2
				case hv, hf:
					n += 3
				}
			}
		}
		sinkInt = n
	}
}

// Scan only, no name work at all: the floor.
func BenchmarkNames_Floor(b *testing.B) {
	b.SetBytes(int64(len(benchSheet)))
	b.ReportAllocs()
	src := bytes.NewReader(benchSheet)
	r := NewReader(src, Options{})
	for range b.N {
		src.Reset(benchSheet)
		r.Reset(src)
		for range r.Tokens() {
		}
	}
}
