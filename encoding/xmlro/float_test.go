package xmlro

import (
	"math"
	"math/rand/v2"
	"strconv"
	"strings"
	"testing"
)

// The fast path must agree with strconv bit for bit wherever it answers, and
// must decline everything it cannot answer exactly.
func TestFloatFastPathAgreesWithStrconv(t *testing.T) {
	fixed := []string{
		"0", "-0", "0.0", "1", "-1", "12.25", "4000", "0.1", "0.2", "0.3", "2.675",
		"1234567.890123", "99999999999999.9", "999999999999999", "0.000000000000000000001",
		"5.", ".5", "-.5", "00012", "1e5", "1E5", "+1", "inf", "NaN", "", "-", ".", "1.2.3",
		"1234567890123456", "0.0000000000000000000001", "0.30000000000000004", "9007199254740993",
	}
	check := func(s string) {
		t.Helper()
		got, ok := parseDecimalFast([]byte(s))
		want, err := strconv.ParseFloat(s, 64)
		if !ok {
			return
		}
		if err != nil {
			t.Errorf("%q: fast path accepted what strconv rejects: %v", s, err)
			return
		}
		if math.Float64bits(got) != math.Float64bits(want) {
			t.Errorf("%q: fast %v (%x), strconv %v (%x)", s, got, math.Float64bits(got), want, math.Float64bits(want))
		}
	}
	for _, s := range fixed {
		check(s)
	}
	rng := rand.New(rand.NewPCG(1, 2))
	for range 200000 {
		var b strings.Builder
		if rng.IntN(4) == 0 {
			b.WriteByte('-')
		}
		nInt := rng.IntN(16)
		nFrac := rng.IntN(20)
		for range nInt {
			b.WriteByte(byte('0' + rng.IntN(10)))
		}
		if nFrac > 0 || rng.IntN(2) == 0 {
			b.WriteByte('.')
		}
		for range nFrac {
			b.WriteByte(byte('0' + rng.IntN(10)))
		}
		check(b.String())
	}
	// The values through the public method, including the fallback.
	for _, c := range []struct {
		in   string
		want float64
	}{{"12.25", 12.25}, {"1e3", 1000}, {"1.2345678901234567890", 1.2345678901234567}} {
		got, err := Value(c.in).Float()
		if err != nil || got != c.want {
			t.Errorf("Value(%q).Float() = %v, %v", c.in, got, err)
		}
	}
	if _, err := Value("12.2x").Float(); err == nil {
		t.Error("junk accepted")
	}
}

var sinkF float64

func BenchmarkFloat_Strconv(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		sinkF, _ = strconv.ParseFloat("12.25", 64)
	}
}

func BenchmarkFloat_Value(b *testing.B) {
	b.ReportAllocs()
	v := Value("12.25")
	for range b.N {
		sinkF, _ = v.Float()
	}
}

func BenchmarkFloat_ValueFallback(b *testing.B) {
	b.ReportAllocs()
	v := Value("1.2345678901234567890")
	for range b.N {
		sinkF, _ = v.Float()
	}
}
