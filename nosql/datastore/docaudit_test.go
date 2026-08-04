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

// TestNotInScopeNamesNothingExported checks the README's not-in-scope list
// against the exported surface.
//
// SUM and AVG sat on that list for a day after shipping, and a consumer read it
// and wrote the paging loop including them was meant to prevent. A list of
// feature names is the one part of prose that can be checked mechanically, and
// it is the part that has actually been wrong.
//
// It deliberately checks nothing else in the README. A check that fails on a
// rewording teaches people to edit around it, which is worse than no check.
func TestNotInScopeNamesNothingExported(t *testing.T) {
	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Skipf("no README here: %v", err)
	}
	section := notInScopeSection(string(readme))
	if section == "" {
		t.Fatal(`no "## Not in scope" section found; the check is not doing anything`)
	}

	out, err := exec.Command("go", "doc", "-all", ".").Output()
	if err != nil {
		t.Skipf("go doc unavailable: %v", err)
	}
	exported := exportedNames(string(out))
	if len(exported) == 0 {
		t.Fatal("no exported names parsed; the check is not doing anything")
	}

	// Only names the list actually claims are absent. Backticked identifiers
	// are how the list spells a feature it is naming.
	for _, m := range regexp.MustCompile("`(\\w+)`").FindAllStringSubmatch(section, -1) {
		if exported[m[1]] {
			t.Errorf("the not-in-scope list names %q, which this package exports", m[1])
		}
	}
	// The list also names features in prose. Check the ones that are also Go
	// identifiers, upper-cased, since that is how a feature turns into API.
	for _, word := range regexp.MustCompile(`\b[A-Z]{2,}\b`).FindAllString(section, -1) {
		name := strings.ToUpper(word[:1]) + strings.ToLower(word[1:])
		if exported[name] {
			t.Errorf("the not-in-scope list names %q, which this package exports as %s", word, name)
		}
	}
}

func notInScopeSection(readme string) string {
	start := strings.Index(readme, "## Not in scope")
	if start < 0 {
		return ""
	}
	rest := readme[start+len("## Not in scope"):]
	if end := strings.Index(rest, "\n## "); end >= 0 {
		return rest[:end]
	}
	return rest
}

func exportedNames(doc string) map[string]bool {
	out := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^func (?:\([^)]*\) )?([A-Z]\w*)`).FindAllStringSubmatch(doc, -1) {
		out[m[1]] = true
	}
	for _, m := range regexp.MustCompile(`(?m)^type ([A-Z]\w*)`).FindAllStringSubmatch(doc, -1) {
		out[m[1]] = true
	}
	return out
}
