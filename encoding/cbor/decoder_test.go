package cbor

import (
	"bytes"
	"errors"
	"io"
	"math"
	"reflect"
	"testing"
)

func decoder(t *testing.T, data []byte, opts DecoderOptions) *Decoder {
	t.Helper()
	d, err := NewDecoder(bytes.NewReader(data), opts)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestDecoderCOSEKeyTokens(t *testing.T) {
	// {1:2, 3:-7, -1:1, -2:h'0102', -3:h'0304'}
	data := []byte{0xa5, 0x01, 0x02, 0x03, 0x26, 0x20, 0x01, 0x21, 0x42, 0x01, 0x02, 0x22, 0x42, 0x03, 0x04}
	d := decoder(t, data, DecoderOptions{})
	pairs, indef, err := d.ReadMap()
	if err != nil || pairs != 5 || indef {
		t.Fatalf("ReadMap = %d, %v, %v", pairs, indef, err)
	}
	wantInts := []int64{1, 2, 3, -7, -1, 1, -2}
	for _, want := range wantInts {
		got, err := d.ReadInt()
		if err != nil || got != want {
			t.Fatalf("ReadInt = %d, %v; want %d", got, err, want)
		}
	}
	if got, err := d.ReadBytes(); err != nil || !bytes.Equal(got, []byte{1, 2}) {
		t.Fatalf("x = %x, %v", got, err)
	}
	if got, err := d.ReadInt(); err != nil || got != -3 {
		t.Fatalf("label = %d, %v", got, err)
	}
	if got, err := d.ReadBytes(); err != nil || !bytes.Equal(got, []byte{3, 4}) {
		t.Fatalf("y = %x, %v", got, err)
	}
	if tok, err := d.ReadToken(); err != nil || tok.Kind != EndMap {
		t.Fatalf("end = %v, %v", tok.Kind, err)
	}
	if _, err := d.ReadToken(); err != io.EOF {
		t.Fatalf("after root = %v", err)
	}
}

func TestDecoderIndefiniteInput(t *testing.T) {
	// [_ "strea" "ming", (_ h'0102', h'03')]
	data := []byte{0x9f, 0x7f, 0x65, 's', 't', 'r', 'e', 'a', 0x64, 'm', 'i', 'n', 'g', 0xff, 0x5f, 0x42, 1, 2, 0x41, 3, 0xff, 0xff}
	d := decoder(t, data, DecoderOptions{})
	if _, indef, err := d.ReadArray(); err != nil || !indef {
		t.Fatalf("array = %v, %v", indef, err)
	}
	if got, err := d.ReadText(); err != nil || got != "streaming" {
		t.Fatalf("text = %q, %v", got, err)
	}
	if got, err := d.ReadBytes(); err != nil || !bytes.Equal(got, []byte{1, 2, 3}) {
		t.Fatalf("bytes = %x, %v", got, err)
	}
	if tok, err := d.ReadToken(); err != nil || tok.Kind != EndArray {
		t.Fatalf("end = %v, %v", tok.Kind, err)
	}
}

func TestDecoderTagAndFloats(t *testing.T) {
	d := decoder(t, []byte{0xd8, 0x18, 0x82, 0xf9, 0x3e, 0x00, 0xfb, 0x40, 0x09, 0x21, 0xfb, 0x54, 0x44, 0x2d, 0x18}, DecoderOptions{})
	if tag, err := d.ReadTag(); err != nil || tag != 24 {
		t.Fatalf("tag = %d, %v", tag, err)
	}
	if n, _, err := d.ReadArray(); err != nil || n != 2 {
		t.Fatalf("array = %d, %v", n, err)
	}
	if v, err := d.ReadFloat(); err != nil || v != 1.5 {
		t.Fatalf("half = %v, %v", v, err)
	}
	if v, err := d.ReadFloat(); err != nil || math.Abs(v-math.Pi) > 1e-15 {
		t.Fatalf("double = %v, %v", v, err)
	}
}

func TestReadRawRejectsMalformedAndTrailing(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want error
	}{
		{"truncated", []byte{0x1a, 0, 0}, ErrTruncated},
		{"reserved additional", []byte{0x1c}, ErrMalformed},
		{"invalid UTF-8", []byte{0x61, 0xff}, ErrMalformed},
		{"unexpected break", []byte{0xff}, ErrMalformed},
		{"trailing", []byte{0x00, 0x01}, ErrExtraneousData},
		{"map value break", []byte{0xbf, 0x00, 0xff}, ErrMalformed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := decoder(t, tc.data, DecoderOptions{}).ReadRaw()
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v; want %v", err, tc.want)
			}
		})
	}
}

func TestReadRawDuplicateMapKeys(t *testing.T) {
	tests := [][]byte{
		{0xa2, 0x00, 0x01, 0x00, 0x02},
		{0xa2, 0x00, 0x01, 0x18, 0x00, 0x02}, // same integer, different encoding
		{0xbf, 0x61, 'a', 0x01, 0x61, 'a', 0x02, 0xff},
		{0xa2, 0x61, 'a', 0x01, 0x7f, 0x61, 'a', 0xff, 0x02},               // definite/indefinite text
		{0xa2, 0x81, 0x00, 0x01, 0x9f, 0x18, 0x00, 0xff, 0x02},             // equivalent array keys
		{0xa2, 0xf9, 0x3e, 0x00, 0x01, 0xfa, 0x3f, 0xc0, 0x00, 0x00, 0x02}, // equivalent float keys
		{0xa2, 0xa1, 0x00, 0x01, 0x01, 0xbf, 0x18, 0x00, 0x01, 0xff, 0x02}, // equivalent map keys
	}
	for _, data := range tests {
		_, err := decoder(t, data, DecoderOptions{RejectDuplicateMapKeys: true}).ReadRaw()
		if !errors.Is(err, ErrDuplicateMapKey) {
			t.Fatalf("%x: %v", data, err)
		}
	}
	if _, err := decoder(t, tests[0], DecoderOptions{}).ReadRaw(); err != nil {
		t.Fatalf("duplicates allowed: %v", err)
	}
}

func TestTokenModeRejectsDuplicateMapKeys(t *testing.T) {
	d := decoder(t, []byte{0xa2, 0x00, 0x01, 0x00, 0x02}, DecoderOptions{RejectDuplicateMapKeys: true})
	if _, err := d.ReadToken(); !errors.Is(err, ErrDuplicateMapKey) {
		t.Fatalf("ReadToken error = %v", err)
	}
}

func TestReadRawEmptyIsEOF(t *testing.T) {
	if _, err := decoder(t, nil, DecoderOptions{}).ReadRaw(); err != io.EOF {
		t.Fatalf("error = %v", err)
	}
}

func TestDecoderLimits(t *testing.T) {
	tests := []struct {
		data []byte
		opts DecoderOptions
	}{
		{[]byte{0x43, 1, 2, 3}, DecoderOptions{MaxStringBytes: 2}},
		{[]byte{0x82, 0, 1}, DecoderOptions{MaxContainerItems: 1}},
		{[]byte{0x81, 0x81, 0}, DecoderOptions{MaxNestedLevels: 1}},
		{[]byte{0xc0, 0xc0, 0x00}, DecoderOptions{MaxNestedLevels: 1}},
		{[]byte{0x1a, 0, 0, 0, 1}, DecoderOptions{MaxInputBytes: 4}},
		{[]byte{0x43, 1, 2, 3}, DecoderOptions{MaxRawMessageBytes: 3}},
	}
	for _, tc := range tests {
		_, err := decoder(t, tc.data, tc.opts).ReadRaw()
		if !errors.Is(err, ErrLimitExceeded) {
			t.Fatalf("%x: %v", tc.data, err)
		}
	}
}

func TestDecoderSequence(t *testing.T) {
	d := decoder(t, []byte{0x01, 0x21}, DecoderOptions{Sequence: true})
	var got []int64
	for {
		v, err := d.ReadInt()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, v)
	}
	if !reflect.DeepEqual(got, []int64{1, -2}) {
		t.Fatalf("got %v", got)
	}
}

func TestNegativeIntegerFullRange(t *testing.T) {
	d := decoder(t, []byte{0x3b, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}, DecoderOptions{})
	tok, err := d.ReadToken()
	if err != nil {
		t.Fatal(err)
	}
	if tok.Kind != NegativeInteger || tok.Argument != math.MaxUint64 {
		t.Fatalf("token = %#v", tok)
	}
	if _, err := tok.Int64(); !errors.Is(err, ErrIntegerOverflow) {
		t.Fatalf("Int64 = %v", err)
	}
}
