# httprevproxy

`httprevproxy` is a TinyGo-compatible reverse proxy whose public API mirrors
the reverse-proxy portion of `net/http/httputil`. The distinct package name
keeps imports unambiguous. Applications that want a later one-line migration
can import it with the `httputil` alias, then change only the import path:

```go
import httputil "github.com/shibukawa/tinygodriver/httprevproxy"

target, _ := url.Parse("http://127.0.0.1:8080/base")
proxy := &httputil.ReverseProxy{
	Rewrite: func(r *httputil.ProxyRequest) {
		r.SetURL(target)
		r.SetXForwarded()
	},
}
http.Handle("/", proxy)
```

Supported behavior includes `Rewrite`, the deprecated `Director`,
`NewSingleHostReverseProxy`, hop-by-hop header removal, forwarding helpers,
response modification, error handling, trailers, bounded-buffer streaming,
and periodic or immediate flushing.

Protocol upgrades (including WebSockets), 1xx forwarding, and HTTP/2-specific
proxy features are intentionally unsupported. A protocol-upgrade request or
response is passed to `ErrorHandler` and produces 502 with the default handler.
