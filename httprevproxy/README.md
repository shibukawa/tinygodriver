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

## `Director` does not work under TinyGo

Use `Rewrite`, as the example above does. TinyGo's HTTP client takes the dial
address from `Request.Host` where standard `net/http` takes it from
`Request.URL.Host` (see `rule:tinygo-dials-request-host`), so preserving the
inbound Host — which is exactly what `Director` and `NewSingleHostReverseProxy`
are for — means dialing it, and the proxy ends up calling itself.

`Rewrite` with `SetURL` is unaffected: `SetURL` leaves `Out.Host` empty, and
this package fills it in from the target URL on the TinyGo path, which is the
same Host header standard `net/http` would have written. A `Rewrite` that sets
`Out.Host` itself is left alone. Only a Host that disagrees with the target URL
is refused, and it is refused through `ErrorHandler` rather than looping.

The stdlib `net/http/httputil.ReverseProxy` that TinyGo 0.42 ships has the same
problem and no such compensation.

Protocol upgrades (including WebSockets), 1xx forwarding, and HTTP/2-specific
proxy features are intentionally unsupported. A protocol-upgrade request or
response is passed to `ErrorHandler` and produces 502 with the default handler.
