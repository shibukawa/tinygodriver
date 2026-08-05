//go:build !tinygo

package datastore_test

import (
	"os"
	"os/exec"
	"path/filepath"
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

// TestNotInScopeNamesNothingExported checks every not-in-scope list this
// package ships against the exported surface.
//
// SUM and AVG sat on the README's list for a day after shipping, and a consumer
// read it and wrote the paging loop including them was meant to prevent. A list
// of feature names is the one part of prose that can be checked mechanically,
// and it is the part that has actually been wrong.
//
// The first version of this test read README.md alone, and the same two names
// survived in requirement:datastore-client-scope's out_of_scope for a further
// tag — twenty lines below the entry recording that they had been added, in the
// file the README's fix cited as its authority. A guard aimed at one artifact
// says nothing about the identical claim in the next one, so this reads both:
// the README section and every out_of_scope block in the catalog.
//
// It deliberately checks nothing else. A check that fails on a rewording
// teaches people to edit around it, which is worse than no check.
func TestNotInScopeNamesNothingExported(t *testing.T) {
	sections := notInScopeSections(t)
	if len(sections) == 0 {
		t.Skip("neither a README nor a catalog here")
	}

	out, err := exec.Command("go", "doc", "-all", ".").Output()
	if err != nil {
		t.Skipf("go doc unavailable: %v", err)
	}
	exported := exportedNames(string(out))
	if len(exported) == 0 {
		t.Fatal("no exported names parsed; the check is not doing anything")
	}

	for _, s := range sections {
		// Only names the list actually claims are absent. Backticked
		// identifiers are how a list spells a feature it is naming.
		for _, m := range regexp.MustCompile("`(\\w+)`").FindAllStringSubmatch(s.text, -1) {
			if exported[m[1]] {
				t.Errorf("%s: the not-in-scope list names %q, which this package exports", s.where, m[1])
			}
		}
		// The lists also name features in prose. Check the ones that are also
		// Go identifiers, upper-cased, since that is how a feature turns into
		// API.
		for _, word := range regexp.MustCompile(`\b[A-Z]{2,}\b`).FindAllString(s.text, -1) {
			name := strings.ToUpper(word[:1]) + strings.ToLower(word[1:])
			if exported[name] {
				t.Errorf("%s: the not-in-scope list names %q, which this package exports as %s", s.where, word, name)
			}
		}
	}
	t.Logf("checked %d not-in-scope lists against the exported surface", len(sections))
}

type scopeSection struct {
	where string
	text  string
}

// notInScopeSections collects the README's prose list and the out_of_scope
// block of every catalog concept about this package.
//
// Two filters, both learned by running the unfiltered version. It compared
// requirement:sql-batch-execution's out_of_scope against this package's names
// and objected to "INSERT", which is SQL there and a coincidence here — a
// concept about another package cannot make a claim about this one's surface,
// so only Datastore concepts are read. And it objected to "COUNT" inside a
// reason explaining why the other aggregations were excluded, which is prose
// naming an in-scope feature to contrast with it. requirement:datastore-doc-
// accuracy already says prose cannot be checked and a guard that fails on a
// rewording is worse than none, so reasons are dropped and the list-shaped
// claims — the keys and their apis values — are what gets read.
func notInScopeSections(t *testing.T) []scopeSection {
	t.Helper()
	var out []scopeSection

	if readme, err := os.ReadFile("README.md"); err == nil {
		if section := notInScopeSection(string(readme)); section != "" {
			out = append(out, scopeSection{"README.md", section})
		} else {
			t.Error(`no "## Not in scope" section in README.md; the check lost its subject`)
		}
	}

	concepts, err := filepath.Glob(filepath.Join("../../.knowledge", "*", "*datastore*.md"))
	if err != nil {
		return out
	}
	for _, path := range concepts {
		body, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if block := yamlBlock(string(body), "out_of_scope:"); block != "" {
			out = append(out, scopeSection{filepath.ToSlash(path), listShapedOnly(block)})
		}
	}
	return out
}

// listShapedOnly drops the prose from a YAML block, keeping the keys and the
// short inline values that name a feature.
//
// Two things count as prose. A folded scalar is prose by construction, whatever
// its key. And an explanation — reason, note, why — is prose even on one line,
// because explaining why something is excluded routinely means naming the
// in-scope feature it is being contrasted with, which is how COUNT tripped the
// unfiltered version.
var (
	yamlKey    = regexp.MustCompile(`^([\w-]+):\s*(.*)$`)
	proseKeys  = map[string]bool{"reason": true, "note": true, "why": true}
	foldedMark = regexp.MustCompile(`^[>|][-+\d]*$`)
)

func listShapedOnly(block string) string {
	var kept []string
	folded := false
	foldedAt := 0
	for _, line := range strings.Split(block, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		key := yamlKey.FindStringSubmatch(trimmed)
		if folded {
			// The block continues until something indented no deeper than
			// the key that opened it.
			if indent > foldedAt {
				continue
			}
			folded = false
		}
		if key == nil {
			kept = append(kept, line)
			continue
		}
		if foldedMark.MatchString(key[2]) {
			folded, foldedAt = true, indent
			continue
		}
		if proseKeys[key[1]] {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
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

// yamlBlock returns the lines under a top-level YAML key, which are the ones
// indented past it. Good enough for the shapes this catalog uses, and a
// mis-parse costs a missed check rather than a false failure.
func yamlBlock(body, key string) string {
	lines := strings.Split(body, "\n")
	start := -1
	for i, line := range lines {
		if strings.TrimRight(line, " \t") == key {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return ""
	}
	var block []string
	for _, line := range lines[start:] {
		if strings.TrimSpace(line) == "" {
			block = append(block, line)
			continue
		}
		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			break
		}
		block = append(block, line)
	}
	return strings.Join(block, "\n")
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
