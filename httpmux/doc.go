// Package httpmux provides an HTTP request multiplexer with the Go 1.22+
// net/http ServeMux pattern syntax.
//
// Normal host Go builds expose ServeMux as an alias of [net/http.ServeMux].
// TinyGo builds and builds using the force_tinygo_logic tag select the
// compatible implementation in this package. Both variants implement
// [net/http.Handler] and expose the same constructor and registration methods.
package httpmux
