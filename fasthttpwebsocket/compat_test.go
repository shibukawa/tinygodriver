// Tests for the fork's divergences from upstream, and for the fasthttp side of
// the library, which upstream does not test at all: its suite is gorilla's,
// vendored whole, and not one of its cases touches FastHTTPUpgrader. Everything
// here runs over real sockets so that on TinyGo it runs over netdev, which is
// the only way to find out whether RequestCtx.Hijack survives there.
//
// Everything here runs under both compilers, which constrains how they report.
// TinyGo's testing has no runtime.Goexit, so t.Fatal does not stop the function
// that called it and t.Skip marks the test *failed* rather than skipped. Every
// failure path therefore reports with t.Error and returns explicitly, and a test
// that does not apply logs why and returns instead of skipping.

package websocket

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shibukawa/tinygodriver/fasthttp"
)

// The environment the proxy shim is expected to read. init runs before any test
// and so before the first defaultProxy call, which is the only moment either
// compiler looks at the environment.
//
// Setting these globally is safe for the rest of the suite because neither
// net/http nor x/net/http/httpproxy ever proxies a loopback address, and every
// server here is on 127.0.0.1. TestDefaultProxy asserts that exemption rather
// than assuming it.
func init() {
	os.Setenv("HTTP_PROXY", "http://proxy.invalid:3128")
	os.Setenv("NO_PROXY", "direct.example.com")
}

// fastwsPort hands out fixed ports. TinyGo's net.Listener.Addr() reports the
// address it was asked for, so binding to :0 never reveals the assigned port;
// see netdev's Device.LocalAddr. The range is the fasthttp fork's plus 200, so
// the two suites can run at the same time.
var fastwsPort int32 = 18500

func fastwsListen(t *testing.T) (net.Listener, string) {
	t.Helper()
	var lastErr error
	for range 40 {
		addr := fmt.Sprintf("127.0.0.1:%d", atomic.AddInt32(&fastwsPort, 1))
		ln, err := net.Listen("tcp", addr)
		if err == nil {
			return ln, addr
		}
		lastErr = err
	}
	t.Errorf("no free port in the test range; last error: %v", lastErr)
	return nil, ""
}

// fastwsServer is a fasthttp server with one upgrade route and one ordinary
// one, so a test can prove both live on the same listener.
type fastwsServer struct {
	addr string
	stop func()

	// upgradeErr holds what Upgrade last returned, for the rejection cases.
	// Upgrade has already written the HTTP response by then.
	upgradeErr atomic.Value
}

func (s *fastwsServer) url(path string) string { return "ws://" + s.addr + path }

func (s *fastwsServer) lastUpgradeErr() error {
	if v, ok := s.upgradeErr.Load().(error); ok {
		return v
	}
	return nil
}

// fastwsServe starts a server whose /ws route upgrades and hands the connection
// to h, and whose /plain route answers ordinary HTTP.
func fastwsServe(t *testing.T, up *FastHTTPUpgrader, h FastHTTPHandler) *fastwsServer {
	t.Helper()
	ln, addr := fastwsListen(t)
	if ln == nil {
		return nil
	}

	s := &fastwsServer{addr: addr}
	handler := func(ctx *fasthttp.RequestCtx) {
		if string(ctx.Path()) != "/ws" {
			ctx.SetBodyString("plain")
			return
		}
		if err := up.Upgrade(ctx, h); err != nil {
			s.upgradeErr.Store(err)
		}
	}

	srv := &fasthttp.Server{
		Handler: handler,
		// The upgrade response itself is written under these; the hijacked
		// connection clears its deadlines afterwards, which is what lets a
		// WebSocket outlive them.
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()
	time.Sleep(150 * time.Millisecond)

	s.stop = func() {
		if err := srv.Shutdown(); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Serve returned %v after Shutdown, want nil", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("Serve did not return after Shutdown")
		}
	}
	return s
}

// echoHandler mirrors whatever it is sent, message type included, until the
// peer closes.
func echoHandler(c *Conn) {
	defer c.Close()
	for {
		mt, r, err := c.NextReader()
		if err != nil {
			return
		}
		w, err := c.NextWriter(mt)
		if err != nil {
			return
		}
		if _, err := io.Copy(w, r); err != nil {
			return
		}
		if err := w.Close(); err != nil {
			return
		}
	}
}

// fastwsDialer leaves NetDialContext nil on purpose, so the default
// (&net.Dialer{}).DialContext path is the one under test. Proxy stays nil:
// TestDefaultProxy covers that shim on its own.
func fastwsDialer() *Dialer {
	return &Dialer{HandshakeTimeout: 10 * time.Second}
}

// dial opens a client connection and gives it deadlines, so a protocol bug
// fails the test instead of hanging it.
func dial(t *testing.T, d *Dialer, rawURL string, h http.Header) (*Conn, *http.Response, bool) {
	t.Helper()
	c, resp, err := d.Dial(rawURL, h)
	if err != nil {
		if resp != nil {
			t.Errorf("Dial %s: %v (status %d)", rawURL, err, resp.StatusCode)
		} else {
			t.Errorf("Dial %s: %v", rawURL, err)
		}
		return nil, resp, false
	}
	if err := c.SetReadDeadline(time.Now().Add(20 * time.Second)); err != nil {
		t.Errorf("SetReadDeadline: %v", err)
	}
	if err := c.SetWriteDeadline(time.Now().Add(20 * time.Second)); err != nil {
		t.Errorf("SetWriteDeadline: %v", err)
	}
	return c, resp, true
}

// roundTrip sends one message and returns the echo.
func roundTrip(t *testing.T, c *Conn, mt int, payload []byte) ([]byte, bool) {
	t.Helper()
	if err := c.WriteMessage(mt, payload); err != nil {
		t.Errorf("WriteMessage(%d bytes): %v", len(payload), err)
		return nil, false
	}
	gotType, got, err := c.ReadMessage()
	if err != nil {
		t.Errorf("ReadMessage after %d bytes: %v", len(payload), err)
		return nil, false
	}
	if gotType != mt {
		t.Errorf("echo type = %d, want %d", gotType, mt)
		return nil, false
	}
	return got, true
}

// TestFastHTTPEcho is the one that answers the question this fork exists for:
// whether a fasthttp hijack carries a WebSocket on TinyGo. net/http's Hijack
// deadlocks under netdev, so if this hangs, the whole path is dead.
//
// The payload sizes walk the frame-length encodings: 1 byte, the 125/126
// boundary where the 16-bit length appears, and 65535/65536 where the 64-bit
// one does.
func TestFastHTTPEcho(t *testing.T) {
	s := fastwsServe(t, &FastHTTPUpgrader{}, echoHandler)
	if s == nil {
		return
	}
	defer s.stop()

	c, resp, ok := dial(t, fastwsDialer(), s.url("/ws"), nil)
	if !ok {
		return
	}
	defer c.Close()

	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusSwitchingProtocols)
	}

	for _, n := range []int{0, 1, 125, 126, 127, 1024, 65535, 65536, 131072} {
		payload := make([]byte, n)
		for i := range payload {
			payload[i] = byte('a' + i%26)
		}
		got, ok := roundTrip(t, c, BinaryMessage, payload)
		if !ok {
			return
		}
		if !bytes.Equal(got, payload) {
			t.Errorf("%d-byte echo differs (got %d bytes)", n, len(got))
		}
	}

	text := strings.Repeat("こんにちは", 100)
	got, ok := roundTrip(t, c, TextMessage, []byte(text))
	if !ok {
		return
	}
	if string(got) != text {
		t.Errorf("text echo differs: %d bytes, want %d", len(got), len(text))
	}
}

// TestFastHTTPFragmented writes one message across several Write calls, which
// the client splits into continuation frames once the write buffer fills.
func TestFastHTTPFragmented(t *testing.T) {
	s := fastwsServe(t, &FastHTTPUpgrader{ReadBufferSize: 256, WriteBufferSize: 256}, echoHandler)
	if s == nil {
		return
	}
	defer s.stop()

	c, _, ok := dial(t, &Dialer{HandshakeTimeout: 10 * time.Second, WriteBufferSize: 256}, s.url("/ws"), nil)
	if !ok {
		return
	}
	defer c.Close()

	w, err := c.NextWriter(TextMessage)
	if err != nil {
		t.Errorf("NextWriter: %v", err)
		return
	}
	var want bytes.Buffer
	for i := range 20 {
		chunk := strings.Repeat(fmt.Sprintf("%02d", i), 100)
		want.WriteString(chunk)
		if _, err := io.WriteString(w, chunk); err != nil {
			t.Errorf("write chunk %d: %v", i, err)
			return
		}
	}
	if err := w.Close(); err != nil {
		t.Errorf("writer Close: %v", err)
		return
	}

	mt, got, err := c.ReadMessage()
	if err != nil {
		t.Errorf("ReadMessage: %v", err)
		return
	}
	if mt != TextMessage {
		t.Errorf("type = %d, want %d", mt, TextMessage)
	}
	if !bytes.Equal(got, want.Bytes()) {
		t.Errorf("echo differs: %d bytes, want %d", len(got), want.Len())
	}
}

// TestFastHTTPPingPong covers the control-frame path in both directions,
// including the pong the peer's read loop sends back on its own.
func TestFastHTTPPingPong(t *testing.T) {
	pinged := make(chan string, 1)
	handler := func(c *Conn) {
		defer c.Close()
		c.SetPingHandler(func(data string) error {
			select {
			case pinged <- data:
			default:
			}
			if err := c.WriteControl(PongMessage, []byte(data), time.Now().Add(5*time.Second)); err != nil {
				return err
			}
			// A data message after the pong, so the client's NextReader returns
			// as soon as both control frames have been dispatched instead of
			// waiting out a deadline.
			return c.WriteMessage(TextMessage, []byte("ponged"))
		})
		// The ping handler only runs from inside a read.
		for {
			if _, _, err := c.NextReader(); err != nil {
				return
			}
		}
	}

	s := fastwsServe(t, &FastHTTPUpgrader{}, handler)
	if s == nil {
		return
	}
	defer s.stop()

	c, _, ok := dial(t, fastwsDialer(), s.url("/ws"), nil)
	if !ok {
		return
	}
	defer c.Close()

	ponged := make(chan string, 1)
	c.SetPongHandler(func(data string) error {
		select {
		case ponged <- data:
		default:
		}
		return nil
	})

	if err := c.WriteControl(PingMessage, []byte("hello"), time.Now().Add(5*time.Second)); err != nil {
		t.Errorf("WriteControl(ping): %v", err)
		return
	}
	// NextReader is what dispatches the incoming pong; it returns on the data
	// message the server sends straight after it.
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, _, err := c.NextReader(); err != nil {
		t.Errorf("NextReader: %v", err)
		return
	}

	select {
	case got := <-pinged:
		if got != "hello" {
			t.Errorf("server saw ping %q, want %q", got, "hello")
		}
	default:
		t.Error("the ping never reached the server")
	}
	select {
	case got := <-ponged:
		if got != "hello" {
			t.Errorf("client saw pong %q, want %q", got, "hello")
		}
	default:
		t.Error("the pong never reached the client")
	}
}

// TestFastHTTPCloseHandshake checks that a close frame carries its code and
// text through a hijacked connection, and that the peer's reply is seen.
func TestFastHTTPCloseHandshake(t *testing.T) {
	closed := make(chan string, 1)
	handler := func(c *Conn) {
		defer c.Close()
		c.SetCloseHandler(func(code int, text string) error {
			select {
			case closed <- fmt.Sprintf("%d %s", code, text):
			default:
			}
			return c.WriteControl(CloseMessage,
				FormatCloseMessage(CloseNormalClosure, "bye"), time.Now().Add(5*time.Second))
		})
		for {
			if _, _, err := c.NextReader(); err != nil {
				return
			}
		}
	}

	s := fastwsServe(t, &FastHTTPUpgrader{}, handler)
	if s == nil {
		return
	}
	defer s.stop()

	c, _, ok := dial(t, fastwsDialer(), s.url("/ws"), nil)
	if !ok {
		return
	}
	defer c.Close()

	msg := FormatCloseMessage(CloseGoingAway, "so long")
	if err := c.WriteControl(CloseMessage, msg, time.Now().Add(5*time.Second)); err != nil {
		t.Errorf("WriteControl(close): %v", err)
		return
	}

	_, _, err := c.ReadMessage()
	if !IsCloseError(err, CloseNormalClosure) {
		t.Errorf("ReadMessage = %v, want a %d close", err, CloseNormalClosure)
	}
	if ce, okc := err.(*CloseError); okc && ce.Text != "bye" {
		t.Errorf("close text = %q, want %q", ce.Text, "bye")
	}

	select {
	case got := <-closed:
		if want := fmt.Sprintf("%d so long", CloseGoingAway); got != want {
			t.Errorf("server saw close %q, want %q", got, want)
		}
	default:
		t.Error("the close frame never reached the server")
	}
}

// TestFastHTTPSubprotocol covers selectSubprotocol, which reads the request
// header through fasthttp rather than net/http.
func TestFastHTTPSubprotocol(t *testing.T) {
	var negotiated atomic.Value
	handler := func(c *Conn) {
		negotiated.Store(c.Subprotocol())
		c.Close()
	}
	up := &FastHTTPUpgrader{Subprotocols: []string{"v2.chat", "v1.chat"}}

	s := fastwsServe(t, up, handler)
	if s == nil {
		return
	}
	defer s.stop()

	d := fastwsDialer()
	d.Subprotocols = []string{"v1.chat", "v3.chat"}
	c, resp, ok := dial(t, d, s.url("/ws"), nil)
	if !ok {
		return
	}
	defer c.Close()

	if got := resp.Header.Get("Sec-WebSocket-Protocol"); got != "v1.chat" {
		t.Errorf("response subprotocol = %q, want %q", got, "v1.chat")
	}
	if got := c.Subprotocol(); got != "v1.chat" {
		t.Errorf("client Subprotocol() = %q, want %q", got, "v1.chat")
	}
	time.Sleep(200 * time.Millisecond)
	if got, _ := negotiated.Load().(string); got != "v1.chat" {
		t.Errorf("server Subprotocol() = %q, want %q", got, "v1.chat")
	}
}

// TestFastHTTPCompression negotiates permessage-deflate and sends something
// compressible, which is also the only test here that links flate.
func TestFastHTTPCompression(t *testing.T) {
	s := fastwsServe(t, &FastHTTPUpgrader{EnableCompression: true}, echoHandler)
	if s == nil {
		return
	}
	defer s.stop()

	d := fastwsDialer()
	d.EnableCompression = true
	c, resp, ok := dial(t, d, s.url("/ws"), nil)
	if !ok {
		return
	}
	defer c.Close()

	ext := resp.Header.Get("Sec-WebSocket-Extensions")
	if !strings.HasPrefix(ext, "permessage-deflate") {
		t.Errorf("Sec-WebSocket-Extensions = %q, want permessage-deflate", ext)
	}

	payload := []byte(strings.Repeat("compressme", 2000))
	got, ok := roundTrip(t, c, TextMessage, payload)
	if !ok {
		return
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("compressed echo differs: %d bytes, want %d", len(got), len(payload))
	}
}

// TestFastHTTPRejects walks the handshake checks. Each one must produce an HTTP
// response rather than a hijacked connection, and Upgrade must report why.
func TestFastHTTPRejects(t *testing.T) {
	s := fastwsServe(t, &FastHTTPUpgrader{}, echoHandler)
	if s == nil {
		return
	}
	defer s.stop()

	const key = "dGhlIHNhbXBsZSBub25jZQ=="
	base := map[string]string{
		"Connection":            "Upgrade",
		"Upgrade":               "websocket",
		"Sec-WebSocket-Version": "13",
		"Sec-WebSocket-Key":     key,
	}
	drop := func(k string) map[string]string {
		h := map[string]string{}
		for hk, hv := range base {
			if hk != k {
				h[hk] = hv
			}
		}
		return h
	}

	cases := []struct {
		name    string
		method  string
		headers map[string]string
		want    int
	}{
		{"post", "POST", base, fasthttp.StatusMethodNotAllowed},
		{"no connection header", "GET", drop("Connection"), fasthttp.StatusBadRequest},
		{"no upgrade header", "GET", drop("Upgrade"), fasthttp.StatusBadRequest},
		{"no version", "GET", drop("Sec-WebSocket-Version"), fasthttp.StatusBadRequest},
		{"no key", "GET", drop("Sec-WebSocket-Key"), fasthttp.StatusBadRequest},
	}

	client := &fasthttp.Client{ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second}
	for _, tc := range cases {
		req, resp := fasthttp.AcquireRequest(), fasthttp.AcquireResponse()
		req.SetRequestURI("http://" + s.addr + "/ws")
		req.Header.SetMethod(tc.method)
		for k, v := range tc.headers {
			req.Header.Set(k, v)
		}
		err := client.Do(req, resp)
		code := resp.StatusCode()
		version := string(resp.Header.Peek("Sec-Websocket-Version"))
		fasthttp.ReleaseRequest(req)
		fasthttp.ReleaseResponse(resp)

		switch {
		case err != nil:
			t.Errorf("%s: %v", tc.name, err)
		case code != tc.want:
			t.Errorf("%s: status = %d, want %d", tc.name, code, tc.want)
		case version != "":
			// responseError sets Sec-Websocket-Version and then calls
			// ctx.Error, which resets the response and drops it again. That is
			// upstream's, identical on both compilers and not a fork
			// divergence; the assertion is here so a future version that fixes
			// it says so rather than passing silently.
			t.Errorf("%s: Sec-Websocket-Version = %q; upstream loses it to ctx.Error", tc.name, version)
		}
		if s.lastUpgradeErr() == nil {
			t.Errorf("%s: Upgrade reported no error", tc.name)
		}
		if _, isHandshake := s.lastUpgradeErr().(HandshakeError); !isHandshake {
			t.Errorf("%s: Upgrade returned %T, want HandshakeError", tc.name, s.lastUpgradeErr())
		}
	}
}

// TestFastHTTPCheckOrigin covers the default same-origin policy, which reads
// Host through fasthttp's own accessor rather than http.Request.Host.
func TestFastHTTPCheckOrigin(t *testing.T) {
	s := fastwsServe(t, &FastHTTPUpgrader{}, echoHandler)
	if s == nil {
		return
	}
	defer s.stop()

	// A matching origin is allowed.
	c, _, ok := dial(t, fastwsDialer(), s.url("/ws"),
		http.Header{"Origin": {"http://" + s.addr}})
	if ok {
		c.Close()
	}

	// A foreign one is not, and the error carries the response.
	_, resp, err := fastwsDialer().Dial(s.url("/ws"),
		http.Header{"Origin": {"http://evil.example.com"}})
	if err == nil {
		t.Error("a cross-origin handshake succeeded")
		return
	}
	if resp == nil {
		t.Errorf("Dial: %v, with no response to inspect", err)
		return
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusForbidden)
	}
}

// TestFastHTTPIsWebSocketUpgrade pins the detector applications route on.
func TestFastHTTPIsWebSocketUpgrade(t *testing.T) {
	seen := make(chan bool, 4)
	handler := func(ctx *fasthttp.RequestCtx) {
		seen <- FastHTTPIsWebSocketUpgrade(ctx)
		ctx.SetBodyString("ok")
	}

	ln, addr := fastwsListen(t)
	if ln == nil {
		return
	}
	srv := &fasthttp.Server{Handler: handler, ReadTimeout: 5 * time.Second}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()
	time.Sleep(150 * time.Millisecond)
	defer func() {
		srv.Shutdown()
		<-done
	}()

	client := &fasthttp.Client{ReadTimeout: 10 * time.Second}
	for _, tc := range []struct {
		name    string
		headers map[string]string
		want    bool
	}{
		{"plain GET", nil, false},
		{"upgrade", map[string]string{"Connection": "Upgrade", "Upgrade": "websocket"}, true},
		{"connection only", map[string]string{"Connection": "Upgrade"}, false},
		{"keep-alive, upgrade", map[string]string{"Connection": "keep-alive, Upgrade", "Upgrade": "websocket"}, true},
	} {
		req, resp := fasthttp.AcquireRequest(), fasthttp.AcquireResponse()
		req.SetRequestURI("http://" + addr + "/x")
		for k, v := range tc.headers {
			req.Header.Set(k, v)
		}
		err := client.Do(req, resp)
		fasthttp.ReleaseRequest(req)
		fasthttp.ReleaseResponse(resp)
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		select {
		case got := <-seen:
			if got != tc.want {
				t.Errorf("%s: FastHTTPIsWebSocketUpgrade = %v, want %v", tc.name, got, tc.want)
			}
		case <-time.After(2 * time.Second):
			t.Errorf("%s: the handler never ran", tc.name)
		}
	}
}

// TestFastHTTPAlongsideHTTP proves an upgrade route and ordinary routes share
// one listener, and that hijacking one connection does not disturb the pooled
// RequestCtx machinery behind the others.
func TestFastHTTPAlongsideHTTP(t *testing.T) {
	s := fastwsServe(t, &FastHTTPUpgrader{}, echoHandler)
	if s == nil {
		return
	}
	defer s.stop()

	client := &fasthttp.Client{ReadTimeout: 10 * time.Second}
	plain := func(when string) {
		req, resp := fasthttp.AcquireRequest(), fasthttp.AcquireResponse()
		defer func() {
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)
		}()
		req.SetRequestURI("http://" + s.addr + "/plain")
		if err := client.Do(req, resp); err != nil {
			t.Errorf("%s: %v", when, err)
			return
		}
		if got := string(resp.Body()); got != "plain" {
			t.Errorf("%s: body = %q, want %q", when, got, "plain")
		}
	}

	// Twice before, so the second request proves keep-alive still works.
	plain("before")
	plain("before, on the pooled connection")

	c, _, ok := dial(t, fastwsDialer(), s.url("/ws"), nil)
	if !ok {
		return
	}
	if got, ok := roundTrip(t, c, TextMessage, []byte("ping")); ok && string(got) != "ping" {
		t.Errorf("echo = %q, want %q", got, "ping")
	}
	c.Close()

	plain("after")
}

// TestFastHTTPConcurrent keeps several hijacked connections alive at once.
// Under TinyGo each is a goroutine blocked in a netdev recv, which is where a
// scheduler that cannot preempt them would show up.
func TestFastHTTPConcurrent(t *testing.T) {
	s := fastwsServe(t, &FastHTTPUpgrader{}, echoHandler)
	if s == nil {
		return
	}
	defer s.stop()

	const clients, each = 8, 20
	var okCount, badCount atomic.Int64
	var firstErr atomic.Value
	var wg sync.WaitGroup
	for id := range clients {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, _, err := fastwsDialer().Dial(s.url("/ws"), nil)
			if err != nil {
				badCount.Add(1)
				firstErr.CompareAndSwap(nil, err.Error())
				return
			}
			defer c.Close()
			c.SetReadDeadline(time.Now().Add(30 * time.Second))
			c.SetWriteDeadline(time.Now().Add(30 * time.Second))
			for i := range each {
				want := fmt.Sprintf("client %d message %d", id, i)
				if err := c.WriteMessage(TextMessage, []byte(want)); err != nil {
					badCount.Add(1)
					firstErr.CompareAndSwap(nil, err.Error())
					return
				}
				_, got, err := c.ReadMessage()
				switch {
				case err != nil:
					badCount.Add(1)
					firstErr.CompareAndSwap(nil, err.Error())
					return
				case string(got) != want:
					badCount.Add(1)
					firstErr.CompareAndSwap(nil, fmt.Sprintf("echo %q, want %q", got, want))
				default:
					okCount.Add(1)
				}
			}
		}()
	}
	wg.Wait()

	if bad := badCount.Load(); bad != 0 {
		t.Errorf("%d exchanges failed (first: %v)", bad, firstErr.Load())
	}
	if got := okCount.Load(); got != clients*each {
		t.Errorf("%d exchanges succeeded, want %d", got, clients*each)
	}
}

// TestFastHTTPServerInitiatedClose covers the direction the echo tests do not:
// the server closing while the client is blocked in a read.
func TestFastHTTPServerInitiatedClose(t *testing.T) {
	handler := func(c *Conn) {
		c.WriteMessage(TextMessage, []byte("goodbye"))
		c.WriteControl(CloseMessage,
			FormatCloseMessage(CloseGoingAway, "shutting down"), time.Now().Add(5*time.Second))
		c.Close()
	}

	s := fastwsServe(t, &FastHTTPUpgrader{}, handler)
	if s == nil {
		return
	}
	defer s.stop()

	c, _, ok := dial(t, fastwsDialer(), s.url("/ws"), nil)
	if !ok {
		return
	}
	defer c.Close()

	if _, got, err := c.ReadMessage(); err != nil {
		t.Errorf("first ReadMessage: %v", err)
		return
	} else if string(got) != "goodbye" {
		t.Errorf("message = %q, want %q", got, "goodbye")
	}

	_, _, err := c.ReadMessage()
	if !IsCloseError(err, CloseGoingAway) {
		t.Errorf("second ReadMessage = %v, want a %d close", err, CloseGoingAway)
	}
}

// TestWSSRefusedOnTinyGo is the safety divergence. TinyGo's tls.Client compiles
// and panics, so the fork has to refuse the dial instead; a panic in a dialer is
// not something an application can defend against.
func TestWSSRefusedOnTinyGo(t *testing.T) {
	ln, addr := fastwsListen(t)
	if ln == nil {
		return
	}
	defer ln.Close()

	// A short handshake timeout, because on standard Go this really does open a
	// TLS session against a listener that will never answer one.
	d := &Dialer{HandshakeTimeout: 2 * time.Second}
	var panicked any
	var err error
	func() {
		defer func() { panicked = recover() }()
		_, _, err = d.Dial("wss://"+addr+"/ws", nil)
	}()

	if panicked != nil {
		t.Errorf("Dial panicked: %v", panicked)
		return
	}
	if err == nil {
		t.Error("a wss:// dial to a plaintext listener succeeded")
		return
	}
	if want := tlsDialError(); want != nil && err != want {
		t.Errorf("Dial = %v, want %v", err, want)
	}
}

// TestCloneTLSConfig pins the hand-written clone on the TinyGo side to what
// (*tls.Config).Clone does: a shallow copy, and an *empty* config for nil,
// which is upstream's own choice because the caller sets ServerName next.
func TestCloneTLSConfig(t *testing.T) {
	orig := &tls.Config{
		ServerName:         "example.com",
		NextProtos:         []string{"http/1.1"},
		InsecureSkipVerify: true,
		MinVersion:         tls.VersionTLS12,
	}
	clone := cloneTLSConfig(orig)
	if clone == orig {
		t.Error("cloneTLSConfig returned the original pointer")
	}
	if clone.ServerName != "example.com" {
		t.Errorf("ServerName = %q, want %q", clone.ServerName, "example.com")
	}
	if !clone.InsecureSkipVerify {
		t.Error("InsecureSkipVerify was not carried over")
	}
	if clone.MinVersion != tls.VersionTLS12 {
		t.Errorf("MinVersion = %d, want %d", clone.MinVersion, tls.VersionTLS12)
	}
	if len(clone.NextProtos) != 1 || clone.NextProtos[0] != "http/1.1" {
		t.Errorf("NextProtos = %v", clone.NextProtos)
	}
	// Writing through the clone is visible in the original: the copy is shallow
	// on both compilers.
	clone.NextProtos[0] = "h2"
	if orig.NextProtos[0] != "h2" {
		t.Error("the copy is deeper than standard Go's, so the two backends differ")
	}
	if got := cloneTLSConfig(nil); got == nil {
		t.Error("cloneTLSConfig(nil) must be an empty config, not nil")
	} else if got.ServerName != "" {
		t.Errorf("cloneTLSConfig(nil).ServerName = %q, want empty", got.ServerName)
	}
}

// TestDefaultProxy pins the proxy shim to net/http's behaviour. TinyGo has no
// http.ProxyFromEnvironment, and a build that quietly ignored HTTP_PROXY would
// present as a dial failure with nothing pointing at the proxy.
//
// The environment comes from this file's init, which runs before the first call
// and so before either implementation caches its answer.
func TestDefaultProxy(t *testing.T) {
	if DefaultDialer.Proxy == nil {
		t.Error("DefaultDialer.Proxy is nil; the environment would be ignored")
		return
	}

	for _, tc := range []struct {
		rawURL string
		want   string
	}{
		{"http://example.com/x", "http://proxy.invalid:3128"},
		{"http://direct.example.com/x", ""}, // NO_PROXY
		{"http://127.0.0.1:18500/x", ""},    // loopback is never proxied
		{"https://example.com/x", ""},       // HTTPS_PROXY is unset
	} {
		req, err := http.NewRequest(http.MethodGet, tc.rawURL, nil)
		if err != nil {
			t.Errorf("NewRequest %s: %v", tc.rawURL, err)
			continue
		}
		got, err := defaultProxy(req)
		if err != nil {
			t.Errorf("%s: %v", tc.rawURL, err)
			continue
		}
		switch {
		case tc.want == "" && got != nil:
			t.Errorf("%s: proxy = %s, want none", tc.rawURL, got)
		case tc.want != "" && got == nil:
			t.Errorf("%s: no proxy, want %s", tc.rawURL, tc.want)
		case tc.want != "" && got.String() != tc.want:
			t.Errorf("%s: proxy = %s, want %s", tc.rawURL, got, tc.want)
		}
	}
}
