//go:build force_tinygo_logic && !tinygo && darwin && !darwinstarttlswith13

package https

import (
	"errors"
	"testing"
)

// Secure Transport has no TLS 1.3, so asking for it must fail rather than
// quietly hand back a weaker connection. The mbedTLS build has no such limit,
// which is exactly what -tags darwinstarttlswith13 buys.
func TestUpgradeTLSRejectsTLS13Request(t *testing.T) {
	srv := newStartTLSServer(t)
	defer srv.close()

	cfg := NewConfig(WithRootCAPEM(srv.CAPEM), WithMinVersion(VersionTLS13))
	conn, err := startTLS(t, srv, cfg)
	if err == nil {
		conn.Close()
		t.Fatal("expected ErrProtocolVersion: Secure Transport cannot do TLS 1.3")
	}
	if !errors.Is(err, ErrProtocolVersion) {
		t.Fatalf("error = %v, want ErrProtocolVersion", err)
	}
}
