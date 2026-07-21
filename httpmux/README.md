# httpmux

`httpmux` exposes the Go 1.22+ `net/http.ServeMux` routing model with a
TinyGo-compatible fallback. It is intended for TinyGo releases whose bundled
`ServeMux` accepts only part of the modern pattern syntax while keeping normal
Go builds on the standard library implementation.

```go
import (
    "net/http"

    "github.com/shibukawa/tinygodriver/httpmux"
)

mux := httpmux.NewServeMux()
mux.HandleFunc("GET /users/{id}", func(w http.ResponseWriter, r *http.Request) {
    _, _ = w.Write([]byte(r.PathValue("id")))
})
http.ListenAndServe(":8080", mux)
```

The supported syntax is `[METHOD ][HOST]/[PATH]`, including literal segments,
`{name}`, terminal `{name...}`, terminal `{$}`, trailing-slash subtrees, GET to
HEAD matching, and segment-wise URL unescaping. Matching is independent of
registration order. Invalid, duplicate, and conflicting registrations panic.

The mux also implements the standard path-cleaning and subtree redirects,
host matching, CONNECT handling, 404/405 selection, and `Allow` headers.
`Handler` deliberately does not populate path values; `ServeHTTP` does.

## Implementation selection

- normal host Go builds make `httpmux.ServeMux` a type alias of
  `net/http.ServeMux`
- TinyGo builds use this package's compatible implementation
- `go build -tags force_tinygo_logic` forces the TinyGo-compatible
  implementation on host Go

This is a behavioral subset, not a replacement for `net/http` package-level
registration. It does not replace `http.DefaultServeMux`, implement
`GODEBUG=httpmuxgo121`, or set `http.Request.Pattern` when the compatible
implementation is selected. Normal host Go builds retain all behavior of the
aliased standard-library type.
