//go:build !tinygo

package s3

import "net/http"

// applyRedirectPolicy stops http.Client from following redirects, so Client.do
// sees them and signs each hop for its new host.
//
// This file covers every host-Go build, including -tags force_tinygo_logic:
// the TinyGo code path is exercised there through a standard http.Client, which
// would otherwise follow redirects that TinyGo's own client never follows.
func applyRedirectPolicy(client *http.Client) {
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
}
