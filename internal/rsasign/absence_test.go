//go:build !tinygo

package rsasign_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoCryptoRSAInTinyGoBinary is the criterion this package exists to meet: a
// TinyGo build that signs must contain no crypto/rsa, crypto/x509 or bigmod
// code. It builds a real binary and reads its symbol table, because that is the
// only place the property is observable.
//
// It asserts symbol absence rather than a size threshold. A threshold drifts
// with every unrelated change and passes for the wrong reason; absence states
// the actual property.
//
// Skipped when tinygo is not installed, so a contributor without it still gets
// a green suite. CI must have it.
func TestNoCryptoRSAInTinyGoBinary(t *testing.T) {
	tinygo, err := exec.LookPath("tinygo")
	if err != nil {
		t.Skip("tinygo not installed; this is the check CI must run")
	}
	nm, err := exec.LookPath("nm")
	if err != nil {
		t.Skip("nm not available")
	}

	binary := filepath.Join(t.TempDir(), "sizecheck")
	build := exec.Command(tinygo, "build", "-o", binary, "./testdata/sizecheck/")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("tinygo build: %v\n%s", err, out)
	}

	// The binary must also work, or "no crypto/rsa symbols" would be satisfied
	// by a program that does nothing.
	run := exec.Command(binary, "testdata/rsa2048.pkcs8.pem")
	out, err := run.CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	got := strings.TrimSpace(string(out))
	if !strings.Contains(got, "siglen=256") {
		t.Fatalf("fixture did not sign: %s", got)
	}
	t.Logf("tinygo fixture: %s", got)

	symbols, err := exec.Command(nm, binary).Output()
	if err != nil {
		t.Fatalf("nm: %v", err)
	}
	table := string(symbols)
	// crypto/rsa and bigmod are the RSA math. They must be absent from every
	// TinyGo build, in this fixture and in a real client alike.
	for _, banned := range []string{"crypto/rsa", "bigmod"} {
		if n := strings.Count(table, banned); n != 0 {
			t.Errorf("%d %q symbols in the tinygo binary; the native backend is not being used",
				n, banned)
		}
	}
	// crypto/x509 is absent here only because this fixture links no TLS. A
	// client that imports https gets about 271 x509 symbols regardless of how
	// it signs — they are certificate error types, not key parsing — so this
	// assertion is scoped to the fixture on purpose and must not be copied to
	// a client binary.
	if n := strings.Count(table, "crypto/x509"); n != 0 {
		t.Errorf("%d crypto/x509 symbols in a fixture that links no TLS", n)
	}
	// The positive half: the native backend really is linked. Without this a
	// build that dropped the signing call entirely would pass.
	if !strings.Contains(table, "SecKey") {
		t.Error("no SecKey symbols; Security.framework is not linked")
	}

	if info, err := os.Stat(binary); err == nil {
		t.Logf("tinygo binary: %d bytes", info.Size())
	}
}
