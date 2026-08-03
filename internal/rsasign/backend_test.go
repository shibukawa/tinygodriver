package rsasign

import "testing"

// TestBackendIsReported names the backend under test in the log, so a run that
// silently selected the wrong file is visible rather than merely green.
func TestBackendIsReported(t *testing.T) {
	t.Logf("rsasign backend = %q", Backend)
	if Backend == "unsupported" {
		t.Skip("no backend on this build")
	}
}
