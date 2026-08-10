package https

import (
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
