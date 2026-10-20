package pgtype

import (
	"bytes"
	std "encoding/hex"
	"errors"
	"testing"
)

type hexTestBytesValuer struct {
	value []byte
	err   error
}

func (v hexTestBytesValuer) BytesValue() ([]byte, error) { return v.value, v.err }

func TestByteaTextHexBuffers(t *testing.T) {
	for _, data := range [][]byte{nil, {}, {0, 0xff, 0x10, 0x7f}, bytes.Repeat([]byte{0, 0x9a, 0xbc, 0xff}, 1024)} {
		want := append([]byte("prefix:\\x"), std.EncodeToString(data)...)
		if data == nil {
			want = nil
		}
		for _, valuer := range []bool{false, true} {
			buf := make([]byte, len("prefix:"), len(want)+16)
			copy(buf, "prefix:")
			var got []byte
			var err error
			if valuer {
				got, err = (encodePlanBytesCodecTextBytesValuer{}).Encode(hexTestBytesValuer{value: data}, buf)
			} else {
				got, err = (encodePlanBytesCodecTextBytes{}).Encode(data, buf)
			}
			if err != nil || !bytes.Equal(got, want) || (got == nil) != (want == nil) {
				t.Fatalf("valuer=%v size=%d: incorrect prefix, nil state or bytea encoding: %v", valuer, len(data), err)
				return
			}
		}
	}
	wantErr := errors.New("valuer failure")
	got, err := (encodePlanBytesCodecTextBytesValuer{}).Encode(hexTestBytesValuer{err: wantErr}, []byte("prefix:"))
	if err != wantErr || got != nil {
		t.Fatal("valuer error was not preserved")
	}
}
