package httpserver

import (
	"errors"
	"net"
	"net/http"
	"strings"
	"time"
)

// DefaultReadHeaderTimeout bounds the request head when neither Config nor the
// http.Server sets a limit. net/http defaults to no limit; this package does
// not, because reading the head is its own job here and an unbounded read is a
// goroutine a stalled client can hold forever.
const DefaultReadHeaderTimeout = 10 * time.Second

// Config tunes the TinyGo serving path. The zero value is valid and is what
// Serve uses.
type Config struct {
	// ShouldBypass reports whether a request must reach the handler over a
	// connection it can hijack. nil means IsUpgrade.
	//
	// Returning true for a request whose handler does not hijack still works,
	// but that handler gives up everything http.Server provides, so keep the
	// predicate narrow.
	ShouldBypass func(*http.Request) bool

	// ReadHeaderTimeout bounds the read of the request head. Zero takes
	// http.Server.ReadHeaderTimeout, and if that is zero too,
	// DefaultReadHeaderTimeout. A negative value means no limit.
	ReadHeaderTimeout time.Duration
}

// IsUpgrade reports whether r asks to switch protocols, which is the default
// bypass predicate. It matches the Connection token rather than a specific
// protocol, so it covers WebSocket without this package knowing what that is.
func IsUpgrade(r *http.Request) bool {
	return headerHasToken(r.Header, "Connection", "upgrade")
}

// headerHasToken reports whether a comma-separated header field carries token.
// Comparison is case-insensitive, per RFC 9110.
func headerHasToken(h http.Header, name, token string) bool {
	for _, value := range h.Values(name) {
		for _, part := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(part), token) {
				return true
			}
		}
	}
	return false
}

// ErrNilServer reports a Serve call with no server to serve.
var ErrNilServer = errors.New("httpserver: nil *http.Server")

// Serve accepts connections on ln and serves them through srv, letting a
// handler hijack. It is srv.Serve(ln) plus whatever the platform needs.
//
// Serve returns whatever ended the accept loop, matching http.Server.Serve,
// which returns ErrServerClosed after Shutdown or Close.
func Serve(ln net.Listener, srv *http.Server) error {
	return ServeConfig(ln, srv, Config{})
}

// ServeConfig is Serve with the serving path configured.
//
// On the TinyGo path ServeConfig replaces srv.Handler with a wrapper before
// serving. It does this rather than serving a copy so that srv.Shutdown and
// srv.Close still govern the connections, and it happens once, before the
// listener is read, so no request observes the change.
func ServeConfig(ln net.Listener, srv *http.Server, cfg Config) error {
	if srv == nil {
		return ErrNilServer
	}
	return serve(ln, srv, cfg.resolve(srv))
}

// ListenAndServe listens on addr and serves h.
func ListenAndServe(addr string, h http.Handler) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return Serve(ln, &http.Server{Handler: h})
}

// resolve fills in the defaults that depend on srv.
func (c Config) resolve(srv *http.Server) Config {
	if c.ShouldBypass == nil {
		c.ShouldBypass = IsUpgrade
	}
	if c.ReadHeaderTimeout == 0 {
		c.ReadHeaderTimeout = srv.ReadHeaderTimeout
	}
	if c.ReadHeaderTimeout == 0 {
		c.ReadHeaderTimeout = DefaultReadHeaderTimeout
	}
	return c
}

// handlerOf resolves the handler the way net/http does.
func handlerOf(srv *http.Server) http.Handler {
	if srv.Handler != nil {
		return srv.Handler
	}
	return http.DefaultServeMux
}
