//go:build !tinygo && !force_tinygo_logic

package httpmux

import "net/http"

// ServeMux is an alias of net/http.ServeMux in normal host Go builds.
type ServeMux = http.ServeMux

// NewServeMux allocates and returns a new ServeMux.
func NewServeMux() *ServeMux {
	return http.NewServeMux()
}
