//go:build tinygo

package aws

import "net/http"

// DisableRedirectFollowing does nothing under TinyGo, whose http.Client returns
// a redirect to the caller instead of following it. http.ErrUseLastResponse
// does not exist there, which is why the policy lives behind a build tag.
func DisableRedirectFollowing(*http.Client) {}
