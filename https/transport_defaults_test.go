package https

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestTransportResolvedDefaults(t *testing.T) {
	tr := &Transport{}
	if got := tr.dialTimeout(); got != 30*time.Second {
		t.Fatalf("dialTimeout = %v, want 30s", got)
	}
	if got := tr.maxIdleConnsPerHost(); got != 2 {
		t.Fatalf("maxIdleConnsPerHost = %d, want 2", got)
	}
	if got := tr.idleConnTimeout(); got != 20*time.Second {
		t.Fatalf("idleConnTimeout = %v, want 20s", got)
	}
}

type closeTrackingBody struct {
	io.Reader
	closed bool
}

func (b *closeTrackingBody) Close() error {
	b.closed = true
	return nil
}

func TestRoundTripNilURL(t *testing.T) {
	tr := &Transport{}

	if _, err := tr.RoundTrip(&http.Request{}); err == nil {
		t.Fatal("RoundTrip with nil URL and nil Body: want error, got nil")
	}

	body := &closeTrackingBody{Reader: strings.NewReader("payload")}
	if _, err := tr.RoundTrip(&http.Request{Body: body}); err == nil {
		t.Fatal("RoundTrip with nil URL: want error, got nil")
	}
	if !body.closed {
		t.Fatal("RoundTrip with nil URL did not close the request body")
	}
}
