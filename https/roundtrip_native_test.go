//go:build tinygo || force_tinygo_logic

package https

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestResponseTimeoutUsesEarlierDeadline(t *testing.T) {
	tr := &Transport{ResponseTimeout: 50 * time.Millisecond}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://example.com", nil)
	if err != nil {
		t.Fatal(err)
	}

	left := time.Until(tr.deadlineFor(req))
	if left <= 0 || left > 100*time.Millisecond {
		t.Fatalf("deadline is %v away, want ResponseTimeout to win", left)
	}
}
