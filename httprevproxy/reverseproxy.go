// Portions Copyright 2011 The Go Authors. See LICENSE for terms.

// Package httprevproxy provides a TinyGo-compatible subset of
// net/http/httputil's reverse proxy API.
//
// The public API intentionally follows net/http/httputil so applications can
// migrate without redesigning proxy configuration when TinyGo supports the
// standard implementation. Protocol upgrades and 1xx forwarding are not
// supported.
package httprevproxy

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
	"sync"
	"time"
)

// A ProxyRequest contains a request to be rewritten by a ReverseProxy.
type ProxyRequest struct {
	// In is the request received by the proxy. Rewrite must not modify In.
	In *http.Request
	// Out is the request sent by the proxy. Rewrite may modify or replace Out.
	Out *http.Request
}

// SetURL routes the outbound request to target, joining target's base path to
// the incoming path. It also makes the outbound Host header use target.Host.
func (r *ProxyRequest) SetURL(target *url.URL) {
	rewriteRequestURL(r.Out, target)
	r.Out.Host = ""
}

// SetXForwarded sets X-Forwarded-For, X-Forwarded-Host, and
// X-Forwarded-Proto from the inbound request. An existing X-Forwarded-For
// value on Out is preserved and appended to.
func (r *ProxyRequest) SetXForwarded() {
	clientIP, _, err := net.SplitHostPort(r.In.RemoteAddr)
	if err == nil {
		prior := r.Out.Header["X-Forwarded-For"]
		if len(prior) > 0 {
			clientIP = strings.Join(prior, ", ") + ", " + clientIP
		}
		r.Out.Header.Set("X-Forwarded-For", clientIP)
	} else {
		r.Out.Header.Del("X-Forwarded-For")
	}
	r.Out.Header.Set("X-Forwarded-Host", r.In.Host)
	if r.In.TLS == nil {
		r.Out.Header.Set("X-Forwarded-Proto", "http")
	} else {
		r.Out.Header.Set("X-Forwarded-Proto", "https")
	}
}

// A BufferPool gets and returns temporary byte slices used when copying a
// response body.
type BufferPool interface {
	Get() []byte
	Put([]byte)
}

// ReverseProxy is an HTTP Handler that proxies requests to another server.
// Its public fields match net/http/httputil.ReverseProxy. This implementation
// does not support protocol upgrades or forwarding 1xx responses.
type ReverseProxy struct {
	Rewrite        func(*ProxyRequest)
	Transport      http.RoundTripper
	FlushInterval  time.Duration
	ErrorLog       *log.Logger
	BufferPool     BufferPool
	ModifyResponse func(*http.Response) error
	ErrorHandler   func(http.ResponseWriter, *http.Request, error)

	// Director is deprecated. Use Rewrite instead. Exactly one of Director or
	// Rewrite must be set.
	Director func(*http.Request)
}

// NewSingleHostReverseProxy returns a proxy that routes requests to target.
// Like net/http/httputil, it uses the deprecated Director field for backwards
// compatibility and preserves the inbound Host header.
func NewSingleHostReverseProxy(target *url.URL) *ReverseProxy {
	return &ReverseProxy{Director: func(req *http.Request) {
		rewriteRequestURL(req, target)
	}}
}

// ServeHTTP implements http.Handler.
func (p *ReverseProxy) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	transport := p.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}

	outreq := req.Clone(req.Context())
	if req.ContentLength == 0 {
		outreq.Body = nil
	}
	if outreq.Body != nil {
		// Request.Clone shares Body with the inbound request. A RoundTripper is
		// allowed to close its request body, so protect the server-owned inbound
		// body just as net/http/httputil does.
		outreq.Body = noopCloseReader{ReadCloser: outreq.Body}
		defer outreq.Body.Close()
	}
	if outreq.Header == nil {
		outreq.Header = make(http.Header)
	}

	if (p.Director != nil) == (p.Rewrite != nil) {
		p.errorHandler()(rw, req, errors.New("ReverseProxy must have exactly one of Director or Rewrite set"))
		return
	}

	if p.Director != nil {
		p.Director(outreq)
		if outreq.Form != nil {
			outreq.URL.RawQuery = cleanQueryParams(outreq.URL.RawQuery)
		}
	}
	outreq.Close = false

	if hasUpgrade(outreq.Header) {
		p.errorHandler()(rw, req, errors.New("httprevproxy: protocol upgrades are not supported"))
		return
	}
	removeHopByHopHeaders(outreq.Header)
	if headerValuesContainsToken(req.Header["Te"], "trailers") {
		outreq.Header.Set("Te", "trailers")
	}

	if p.Rewrite != nil {
		outreq.Header.Del("Forwarded")
		outreq.Header.Del("X-Forwarded-For")
		outreq.Header.Del("X-Forwarded-Host")
		outreq.Header.Del("X-Forwarded-Proto")
		outreq.URL.RawQuery = cleanQueryParams(outreq.URL.RawQuery)

		proxyReq := &ProxyRequest{In: req, Out: outreq}
		p.Rewrite(proxyReq)
		outreq = proxyReq.Out
		if outreq == nil {
			p.errorHandler()(rw, req, errors.New("httprevproxy: Rewrite set Out to nil"))
			return
		}
	} else {
		setDirectorXForwardedFor(outreq, req.RemoteAddr)
	}

	if _, ok := outreq.Header["User-Agent"]; !ok {
		outreq.Header.Set("User-Agent", "")
	}

	res, err := transport.RoundTrip(outreq)
	if err != nil {
		p.errorHandler()(rw, outreq, err)
		return
	}
	if res == nil {
		p.errorHandler()(rw, outreq, errors.New("httprevproxy: RoundTripper returned a nil response"))
		return
	}
	if res.Body == nil {
		res.Body = http.NoBody
	}
	defer res.Body.Close()

	if res.StatusCode == http.StatusSwitchingProtocols {
		p.errorHandler()(rw, outreq, errors.New("httprevproxy: protocol upgrades are not supported"))
		return
	}

	removeHopByHopHeaders(res.Header)
	if p.ModifyResponse != nil {
		if err := p.ModifyResponse(res); err != nil {
			p.errorHandler()(rw, outreq, err)
			return
		}

	}

	copyHeader(rw.Header(), res.Header)
	announcedTrailers := len(res.Trailer)
	if announcedTrailers > 0 {
		keys := make([]string, 0, announcedTrailers)
		for key := range res.Trailer {
			keys = append(keys, key)
		}
		rw.Header().Add("Trailer", strings.Join(keys, ", "))
	}
	rw.WriteHeader(res.StatusCode)

	if err := p.copyResponse(rw, res.Body, p.flushInterval(res)); err != nil {
		p.logf("httprevproxy: response body copy error: %v", err)
		return
	}
	if err := res.Body.Close(); err != nil {
		p.logf("httprevproxy: response body close error: %v", err)
	}

	if len(res.Trailer) == announcedTrailers {
		copyHeader(rw.Header(), res.Trailer)
		return
	}
	for key, values := range res.Trailer {
		for _, value := range values {
			rw.Header().Add(http.TrailerPrefix+key, value)
		}
	}
}

func (p *ReverseProxy) errorHandler() func(http.ResponseWriter, *http.Request, error) {
	if p.ErrorHandler != nil {
		return p.ErrorHandler
	}
	return func(rw http.ResponseWriter, req *http.Request, err error) {
		p.logf("http: proxy error: %v", err)
		rw.WriteHeader(http.StatusBadGateway)
	}
}

func (p *ReverseProxy) flushInterval(res *http.Response) time.Duration {
	if isEventStream(res.Header.Get("Content-Type")) {
		return -1
	}
	if res.ContentLength == -1 {
		return -1
	}
	return p.FlushInterval
}

// isEventStream reports a text/event-stream media type. This is the one
// question this package ever asks of a Content-Type, so it compares the media
// type directly rather than linking the mime package's full parser.
func isEventStream(contentType string) bool {
	mediaType := contentType
	if i := strings.IndexByte(mediaType, ';'); i >= 0 {
		mediaType = mediaType[:i]
	}
	return strings.EqualFold(strings.TrimSpace(mediaType), "text/event-stream")
}

func (p *ReverseProxy) copyResponse(dst http.ResponseWriter, src io.Reader, interval time.Duration) error {
	var writer io.Writer = dst
	if interval != 0 {
		if flusher, ok := dst.(http.Flusher); ok {
			latencyWriter := newMaxLatencyWriter(dst, flusher, interval)
			defer latencyWriter.stop()
			writer = latencyWriter
		}
	}

	var buffer []byte
	if p.BufferPool != nil {
		buffer = p.BufferPool.Get()
		defer p.BufferPool.Put(buffer)
	}
	if len(buffer) == 0 {
		buffer = make([]byte, 32*1024)
	}
	_, err := io.CopyBuffer(writer, src, buffer)
	if err == context.Canceled {
		return nil
	}
	return err
}

func (p *ReverseProxy) logf(format string, args ...any) {
	if p.ErrorLog != nil {
		p.ErrorLog.Printf(format, args...)
		return
	}
	log.Printf(format, args...)
}

func setDirectorXForwardedFor(outreq *http.Request, remoteAddr string) {
	clientIP, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return
	}
	prior, exists := outreq.Header["X-Forwarded-For"]
	if exists && prior == nil {
		return
	}
	if len(prior) > 0 {
		clientIP = strings.Join(prior, ", ") + ", " + clientIP
	}
	outreq.Header.Set("X-Forwarded-For", clientIP)
}

func hasUpgrade(header http.Header) bool {
	return header.Get("Upgrade") != "" || headerValuesContainsToken(header["Connection"], "upgrade")
}

func headerValuesContainsToken(values []string, token string) bool {
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			if strings.EqualFold(textproto.TrimString(part), token) {
				return true
			}
		}
	}
	return false
}

func copyHeader(dst, src http.Header) {
	for key, values := range src {
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

var hopHeaders = []string{
	"Connection",
	"Proxy-Connection",
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"Te",
	"Trailer",
	"Transfer-Encoding",
	"Upgrade",
}

func removeHopByHopHeaders(header http.Header) {
	for _, value := range header["Connection"] {
		for _, name := range strings.Split(value, ",") {
			if name = textproto.TrimString(name); name != "" {
				header.Del(name)
			}
		}
	}
	for _, name := range hopHeaders {
		header.Del(name)
	}
}

func rewriteRequestURL(req *http.Request, target *url.URL) {
	targetQuery := target.RawQuery
	req.URL.Scheme = target.Scheme
	req.URL.Host = target.Host
	req.URL.Path, req.URL.RawPath = joinURLPath(target, req.URL)
	if targetQuery == "" || req.URL.RawQuery == "" {
		req.URL.RawQuery = targetQuery + req.URL.RawQuery
	} else {
		req.URL.RawQuery = targetQuery + "&" + req.URL.RawQuery
	}
}

func joinURLPath(a, b *url.URL) (path, rawPath string) {
	if a.RawPath == "" && b.RawPath == "" {
		return singleJoiningSlash(a.Path, b.Path), ""
	}
	aPath := a.EscapedPath()
	bPath := b.EscapedPath()
	aSlash := strings.HasSuffix(aPath, "/")
	bSlash := strings.HasPrefix(bPath, "/")
	switch {
	case aSlash && bSlash:
		return a.Path + b.Path[1:], aPath + bPath[1:]
	case !aSlash && !bSlash:
		return a.Path + "/" + b.Path, aPath + "/" + bPath
	default:
		return a.Path + b.Path, aPath + bPath
	}
}

func singleJoiningSlash(a, b string) string {
	aSlash := strings.HasSuffix(a, "/")
	bSlash := strings.HasPrefix(b, "/")
	switch {
	case aSlash && bSlash:
		return a + b[1:]
	case !aSlash && !bSlash:
		return a + "/" + b
	default:
		return a + b
	}
}

func cleanQueryParams(query string) string {
	if strings.Contains(query, ";") || hasInvalidPercentEncoding(query) {
		values, _ := url.ParseQuery(query)
		return values.Encode()
	}
	return query
}

func hasInvalidPercentEncoding(value string) bool {
	for i := 0; i < len(value); i++ {
		if value[i] != '%' {
			continue
		}
		if i+2 >= len(value) || !isHex(value[i+1]) || !isHex(value[i+2]) {
			return true
		}
		i += 2
	}
	return false
}

func isHex(value byte) bool {
	return value >= '0' && value <= '9' || value >= 'a' && value <= 'f' || value >= 'A' && value <= 'F'
}

type noopCloseReader struct {
	io.ReadCloser
}

func (noopCloseReader) Close() error { return nil }

type maxLatencyWriter struct {
	dst     io.Writer
	flusher http.Flusher
	latency time.Duration
	mu      sync.Mutex
	timer   *time.Timer
	stopped bool
	pending bool
}

func newMaxLatencyWriter(dst io.Writer, flusher http.Flusher, latency time.Duration) *maxLatencyWriter {
	w := &maxLatencyWriter{dst: dst, flusher: flusher, latency: latency, pending: true}
	if latency < 0 {
		w.flusher.Flush()
		w.pending = false
	} else {
		w.timer = time.AfterFunc(latency, w.delayedFlush)
	}
	return w
}

func (w *maxLatencyWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.dst.Write(data)
	if w.latency < 0 {
		w.flusher.Flush()
		return n, err
	}
	if !w.pending && !w.stopped {
		w.pending = true
		w.timer.Reset(w.latency)
	}
	return n, err
}

func (w *maxLatencyWriter) delayedFlush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopped {
		return
	}
	w.flusher.Flush()
	w.pending = false
}

func (w *maxLatencyWriter) stop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.stopped = true
	if w.timer != nil {
		w.timer.Stop()
	}
}
