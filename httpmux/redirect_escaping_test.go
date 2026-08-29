//go:build go1.27 || tinygo || force_tinygo_logic

// Redirect targets keep the escaping the client sent. This package always
// behaves this way; net/http only started to in Go 1.27, which is why the
// build tag is here. Before that the standard mux built the Location from the
// unescaped path in one branch and from the escaped path in the other, and got
// a different wrong answer from each, so on an older toolchain the standard-Go
// path would fail this and the TinyGo path would pass.

package httpmux

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// A redirect Location must carry the path the client asked for, escapes and
// all. Building it from the unescaped path turns "%2F" into a separator and
// names a different resource; building it from the escaped path without saying
// so escapes the "%" again and names one that does not exist.
func TestRedirectPreservesEscaping(t *testing.T) {
	mux := NewServeMux()
	noContent := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }
	mux.HandleFunc("GET /a%2Fb/", noContent)
	mux.HandleFunc("GET /%41/", noContent)
	mux.HandleFunc("GET /literal/a%2Fb", noContent)

	tests := []struct {
		target   string
		location string
	}{
		// Appending the trailing slash keeps the escaping.
		{target: "/a%2Fb", location: "/a%2Fb/"},
		{target: "/a%2Fb?x=1", location: "/a%2Fb/?x=1"},
		{target: "/%41", location: "/%41/"},
		// Cleaning the path keeps it too.
		{target: "/x/../literal/a%2Fb", location: "/literal/a%2Fb"},
		{target: "//literal/a%2Fb", location: "/literal/a%2Fb"},
	}
	for _, test := range tests {
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, httptest.NewRequest("GET", test.target, nil))
		if rr.Code != http.StatusTemporaryRedirect || rr.Header().Get("Location") != test.location {
			t.Errorf("%q: got status %d Location %q, want %d Location %q",
				test.target, rr.Code, rr.Header().Get("Location"),
				http.StatusTemporaryRedirect, test.location)
		}
	}
}
