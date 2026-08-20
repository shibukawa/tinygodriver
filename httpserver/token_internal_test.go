package httpserver

import (
	"net/http"
	"strings"
	"testing"
)

// headerHasTokenReference is the strings.Split implementation headerHasToken
// used before it switched to in-place slicing. The test below locks the two
// to the same answers.
func headerHasTokenReference(h http.Header, name, token string) bool {
	for _, value := range h.Values(name) {
		for _, part := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(part), token) {
				return true
			}
		}
	}
	return false
}

func TestHeaderHasTokenMatchesSplitReference(t *testing.T) {
	cases := []struct {
		name   string
		values []string
	}{
		{"single token", []string{"upgrade"}},
		{"mixed case", []string{"UpGrAdE"}},
		{"leading and trailing spaces", []string{"   upgrade   "}},
		{"tabs around token", []string{"\tupgrade\t"}},
		{"keep-alive then upgrade", []string{"keep-alive, upgrade"}},
		{"no space after comma", []string{"keep-alive,upgrade"}},
		{"keep-alive only", []string{"keep-alive"}},
		{"token with suffix", []string{"upgraded"}},
		{"token with prefix", []string{"reupgrade"}},
		{"internal space", []string{"up grade"}},
		{"empty value", []string{""}},
		{"only commas", []string{",,,"}},
		{"empty elements around token", []string{", upgrade ,"}},
		{"token split across values", []string{"upg", "rade"}},
		{"second value carries token", []string{"keep-alive", "close, upgrade"}},
		{"unicode whitespace", []string{" upgrade "}},
		{"trailing comma", []string{"upgrade,"}},
		{"leading comma", []string{",upgrade"}},
		{"many tokens no match", []string{"a, b, c, d, keep-alive, close"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := http.Header{"Connection": tc.values}
			got := headerHasToken(h, "Connection", "upgrade")
			want := headerHasTokenReference(h, "Connection", "upgrade")
			if got != want {
				t.Errorf("headerHasToken(%q) = %v, reference = %v", tc.values, got, want)
			}
		})
	}
}
