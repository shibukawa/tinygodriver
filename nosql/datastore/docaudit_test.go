//go:build !tinygo

package datastore_test

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// TestConceptMatchesTheCode checks the api:datastore-client concept against the
// declarations it describes.
//
// It exists because that concept has now been wrong three times: a downstream
// reader found four stale entries in the released tag, found a fifth in
// data:datastore-value, and this check found a sixth the same day it was
// written. The .knowledge catalog ships inside the tag, so a stale entry is
// released documentation, and the failure mode is always the same — the concept
// was written as a design before the code existed and nobody re-read it after.
//
// Prose cannot be checked mechanically. Signatures can, so those are what this
// asserts: every "func (c *Client) X(...)" the concept lists must exist, and
// must agree about whether it is variadic.
func TestConceptMatchesTheCode(t *testing.T) {
	const conceptPath = "../../.knowledge/api/datastore-client.md"
	concept, err := os.ReadFile(conceptPath)
	if err != nil {
		t.Skipf("no concept catalog here: %v", err)
	}
	out, err := exec.Command("go", "doc", "-all", ".").Output()
	if err != nil {
		t.Skipf("go doc unavailable: %v", err)
	}
	doc := string(out)

	claimed := regexp.MustCompile(`func \(c \*Client\) (\w+)\(([^)]*)\)`)
	found := 0
	for _, m := range claimed.FindAllStringSubmatch(string(concept), -1) {
		name, args := m[1], m[2]
		real := regexp.MustCompile(`(?m)^func \(c \*Client\) ` + name + `\(([^)]*)\)`).FindStringSubmatch(doc)
		if real == nil {
			t.Errorf("the concept documents (*Client).%s, which does not exist", name)
			continue
		}
		found++
		if strings.Contains(args, "...") != strings.Contains(real[1], "...") {
			t.Errorf("(*Client).%s: concept says %q, code says %q", name, args, real[1])
		}
	}
	if found == 0 {
		t.Fatal("no client methods parsed out of the concept; the check is not doing anything")
	}
	t.Logf("checked %d client methods against the declarations", found)
}
