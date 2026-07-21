//go:build !tinygo && !force_tinygo_logic

package httpmux

import "net/http"

// These assignments fail to compile if the host implementation stops being a
// true type alias of net/http.ServeMux.
var (
	_ *http.ServeMux = NewServeMux()
	_ *ServeMux      = http.NewServeMux()
)
