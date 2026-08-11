//go:build !tinygo

// The one test that can say whether the TinyGo encoder actually emits zstd.
// -tags force_tinygo_logic selects zstd_tinygo.go on standard Go, where
// klauspost's decoder still links, so this reads back what that build put on
// the wire. Under TinyGo itself nothing can decode, which is exactly why the
// check has to live here.
//
// Without the tag this decodes klauspost's own output, which is a weaker but
// still real check that the server negotiated zstd at all.

package fasthttp

import (
	"strings"
	"testing"

	kzstd "github.com/klauspost/compress/zstd"
)

func TestZstdWireFormat(t *testing.T) {
	if !zstdAvailable {
		t.Skip("this build excludes zstd")
	}
	addr, stop := serve(t, CompressHandlerBrotliLevel(testHandler, 4, 6))
	if addr == "" {
		return
	}
	defer stop()

	req, resp := AcquireRequest(), AcquireResponse()
	defer func() {
		ReleaseRequest(req)
		ReleaseResponse(resp)
	}()
	req.SetRequestURI("http://" + addr + "/compressme")
	req.Header.Set(HeaderAcceptEncoding, "zstd")
	if err := testClient().Do(req, resp); err != nil {
		t.Fatalf("zstd request: %v", err)
	}
	if got := string(resp.Header.Peek(HeaderContentEncoding)); got != "zstd" {
		t.Fatalf("Content-Encoding = %q, want %q", got, "zstd")
	}

	dec, err := kzstd.NewReader(nil)
	if err != nil {
		t.Fatalf("decoder: %v", err)
	}
	defer dec.Close()
	decoded, err := dec.DecodeAll(resp.Body(), nil)
	if err != nil {
		t.Fatalf("decode %d bytes from %s: %v", len(resp.Body()), Backend, err)
	}
	want := strings.Repeat("compressme", 200)
	if string(decoded) != want {
		t.Errorf("decoded %d bytes, want %d", len(decoded), len(want))
	}
	if len(resp.Body()) >= len(want) {
		t.Errorf("%d bytes on the wire, no smaller than the %d-byte body",
			len(resp.Body()), len(want))
	}
}

// TestZstdWriterPoolReuse drives the pooled encoder through more responses than
// the pool starts with, which is where a Reset that forgot to clear the frame
// state would show up as a second frame header inside the first frame.
func TestZstdWriterPoolReuse(t *testing.T) {
	if !zstdAvailable {
		t.Skip("this build excludes zstd")
	}
	dec, err := kzstd.NewReader(nil)
	if err != nil {
		t.Fatalf("decoder: %v", err)
	}
	defer dec.Close()

	for i := range 8 {
		want := strings.Repeat("pooled", 40*(i+1))
		encoded := AppendZstdBytes(nil, []byte(want))
		decoded, err := dec.DecodeAll(encoded, nil)
		if err != nil {
			t.Fatalf("round %d: decode: %v", i, err)
		}
		if string(decoded) != want {
			t.Fatalf("round %d: decoded %d bytes, want %d", i, len(decoded), len(want))
		}
	}
}
