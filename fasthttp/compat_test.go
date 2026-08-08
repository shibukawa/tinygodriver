// Tests for the fork's divergences from upstream fasthttp. Upstream's own suite
// is not vendored, so these cover the seams PATCHES.md describes rather than
// fasthttp's behaviour in general -- plus enough of a round trip to prove the
// patched sources still serve and consume HTTP.
//
// Everything here runs under both compilers. TinyGo's testing has no
// runtime.Goexit, so t.Fatal does not stop the function: every failure path
// reports with t.Error and returns explicitly.

package fasthttp

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// nextPort hands out fixed ports. TinyGo's net.Listener.Addr() reports the
// address it was asked for, so binding to :0 never reveals the assigned port;
// see netdev's Device.LocalAddr.
var portCounter int32 = 18300

func listenAny(t *testing.T) (net.Listener, string) {
	t.Helper()
	var lastErr error
	for range 40 {
		addr := fmt.Sprintf("127.0.0.1:%d", atomic.AddInt32(&portCounter, 1))
		ln, err := net.Listen("tcp", addr)
		if err == nil {
			return ln, addr
		}
		lastErr = err
	}
	t.Errorf("no free port in the test range; last error: %v", lastErr)
	return nil, ""
}

func testHandler(ctx *RequestCtx) {
	switch string(ctx.Path()) {
	case "/plain":
		ctx.SetBodyString("plain")
	case "/compressme":
		ctx.SetContentType("text/plain")
		ctx.SetBodyString(strings.Repeat("compressme", 200))
	case "/stream":
		ctx.SetBodyStreamWriter(func(w *bufio.Writer) {
			for i := range 5 {
				fmt.Fprintf(w, "chunk%d\n", i)
				if w.Flush() != nil {
					return
				}
			}
		})
	default:
		ctx.Error("not found", StatusNotFound)
	}
}

// serve starts a server on a fresh port and returns its address plus a stop
// function. Shutdown must make Serve return nil, which is what netdev's
// ErrClosed buys: a raw errno there reads as a crash.
func serve(t *testing.T, h RequestHandler) (addr string, stop func()) {
	t.Helper()
	ln, addr := listenAny(t)
	if ln == nil {
		return "", func() {}
	}
	srv := &Server{Handler: h, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()
	time.Sleep(150 * time.Millisecond)

	return addr, func() {
		if err := srv.Shutdown(); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Serve returned %v after Shutdown, want nil", err)
			}
		case <-time.After(3 * time.Second):
			t.Error("Serve did not return after Shutdown")
		}
	}
}

func testClient() *Client {
	// An explicit Dial keeps the client off TCPDialer's DNS path, which the
	// dedicated test below covers on its own.
	return &Client{
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		Dial:         func(addr string) (net.Conn, error) { return net.Dial("tcp", addr) },
	}
}

// get returns the status and body, or reports and returns ok=false.
func get(t *testing.T, c *Client, url string, headers map[string]string) (int, []byte, bool) {
	t.Helper()
	req, resp := AcquireRequest(), AcquireResponse()
	defer func() {
		ReleaseRequest(req)
		ReleaseResponse(resp)
	}()
	req.SetRequestURI(url)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if err := c.Do(req, resp); err != nil {
		t.Errorf("GET %s: %v", url, err)
		return 0, nil, false
	}
	return resp.StatusCode(), append([]byte(nil), resp.Body()...), true
}

func TestRoundTrip(t *testing.T) {
	addr, stop := serve(t, testHandler)
	if addr == "" {
		return
	}
	defer stop()
	c := testClient()

	code, body, ok := get(t, c, "http://"+addr+"/plain", nil)
	if !ok {
		return
	}
	if code != StatusOK {
		t.Errorf("status = %d, want %d", code, StatusOK)
	}
	if string(body) != "plain" {
		t.Errorf("body = %q, want %q", body, "plain")
	}

	if code, _, ok := get(t, c, "http://"+addr+"/nope", nil); ok && code != StatusNotFound {
		t.Errorf("status = %d, want %d", code, StatusNotFound)
	}
}

// TestChunkedStream covers SetBodyStreamWriter, whose writes go through the
// patched copyZeroAlloc.
func TestChunkedStream(t *testing.T) {
	addr, stop := serve(t, testHandler)
	if addr == "" {
		return
	}
	defer stop()

	_, body, ok := get(t, testClient(), "http://"+addr+"/stream",
		map[string]string{HeaderAcceptEncoding: "identity"})
	if !ok {
		return
	}
	const want = "chunk0\nchunk1\nchunk2\nchunk3\nchunk4\n"
	if string(body) != want {
		t.Errorf("body = %q, want %q", body, want)
	}
}

// TestCompressionRoundTrip checks all four codecs. zstd needs -tags noasm under
// TinyGo, which cannot link klauspost/compress's arm64 assembly, so a failure
// here is the first sign that tag was forgotten.
func TestCompressionRoundTrip(t *testing.T) {
	addr, stop := serve(t, CompressHandlerBrotliLevel(testHandler, 4, 6))
	if addr == "" {
		return
	}
	defer stop()
	c := testClient()
	want := strings.Repeat("compressme", 200)

	for _, enc := range []string{"gzip", "deflate", "br", "zstd"} {
		req, resp := AcquireRequest(), AcquireResponse()
		req.SetRequestURI("http://" + addr + "/compressme")
		req.Header.Set(HeaderAcceptEncoding, enc)
		if err := c.Do(req, resp); err != nil {
			t.Errorf("%s: %v", enc, err)
			ReleaseRequest(req)
			ReleaseResponse(resp)
			continue
		}
		if got := string(resp.Header.Peek(HeaderContentEncoding)); got != enc {
			t.Errorf("%s: Content-Encoding = %q", enc, got)
		}
		var decoded []byte
		var err error
		switch enc {
		case "gzip":
			decoded, err = resp.BodyGunzip()
		case "deflate":
			decoded, err = resp.BodyInflate()
		case "br":
			decoded, err = resp.BodyUnbrotli()
		case "zstd":
			decoded, err = resp.BodyUnzstd()
		}
		switch {
		case err != nil:
			t.Errorf("%s: decode: %v", enc, err)
		case string(decoded) != want:
			t.Errorf("%s: decoded %d bytes, want %d", enc, len(decoded), len(want))
		case len(resp.Body()) >= len(want):
			t.Errorf("%s: %d bytes on the wire, no smaller than the %d-byte body",
				enc, len(resp.Body()), len(want))
		}
		ReleaseRequest(req)
		ReleaseResponse(resp)
	}
}

// TestFSHandler exercises copyZeroAlloc on a file large enough to need more than
// one write, which is where upstream would reach for sendfile.
func TestFSHandler(t *testing.T) {
	dir := t.TempDir()
	content := bytes.Repeat([]byte("filedata"), 100000) // 800 KB
	if err := os.WriteFile(filepath.Join(dir, "data.bin"), content, 0o644); err != nil {
		t.Errorf("WriteFile: %v", err)
		return
	}

	addr, stop := serve(t, FSHandler(dir, 0))
	if addr == "" {
		return
	}
	defer stop()
	c := testClient()
	identity := map[string]string{HeaderAcceptEncoding: "identity"}

	_, body, ok := get(t, c, "http://"+addr+"/data.bin", identity)
	if !ok {
		return
	}
	if !bytes.Equal(body, content) {
		t.Errorf("served %d bytes, want %d", len(body), len(content))
	}

	req, resp := AcquireRequest(), AcquireResponse()
	defer func() {
		ReleaseRequest(req)
		ReleaseResponse(resp)
	}()
	req.SetRequestURI("http://" + addr + "/data.bin")
	req.Header.Set(HeaderAcceptEncoding, "identity")
	req.Header.Set("Range", "bytes=0-9")
	if err := c.Do(req, resp); err != nil {
		t.Errorf("range request: %v", err)
		return
	}
	if resp.StatusCode() != StatusPartialContent {
		t.Errorf("status = %d, want %d", resp.StatusCode(), StatusPartialContent)
	}
	if got := string(resp.Body()); got != "filedatafi" {
		t.Errorf("range body = %q", got)
	}
}

// TestConcurrentRequests would show netdev's listen backlog being too small:
// with a queue of 5, a burst like this drops connections and the server resets
// them. It also keeps the pooled RequestCtx path under real contention.
func TestConcurrentRequests(t *testing.T) {
	addr, stop := serve(t, testHandler)
	if addr == "" {
		return
	}
	defer stop()

	const workers, each = 16, 25
	var okCount, badCount atomic.Int64
	var firstErr atomic.Value
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := testClient()
			for range each {
				req, resp := AcquireRequest(), AcquireResponse()
				req.SetRequestURI("http://" + addr + "/plain")
				switch err := c.Do(req, resp); {
				case err != nil:
					badCount.Add(1)
					firstErr.CompareAndSwap(nil, err.Error())
				case resp.StatusCode() == StatusOK:
					okCount.Add(1)
				default:
					badCount.Add(1)
				}
				ReleaseRequest(req)
				ReleaseResponse(resp)
			}
		}()
	}
	wg.Wait()

	if bad := badCount.Load(); bad != 0 {
		t.Errorf("%d of %d requests failed (first: %v)", bad, workers*each, firstErr.Load())
	}
	if got := okCount.Load(); got != workers*each {
		t.Errorf("%d succeeded, want %d", got, workers*each)
	}
}

// TestDeferredNameResolution drives TCPDialer's own path, with no Dial supplied.
// On TinyGo that means resolveInDialer sends the host to netdev; on standard Go
// it means net.DefaultResolver. Both must reach the server by name.
func TestDeferredNameResolution(t *testing.T) {
	addr, stop := serve(t, testHandler)
	if addr == "" {
		return
	}
	defer stop()

	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Errorf("SplitHostPort: %v", err)
		return
	}
	c := &Client{ReadTimeout: 10 * time.Second}
	for _, host := range []string{"127.0.0.1", "localhost"} {
		code, body, ok := get(t, c, "http://"+host+":"+port+"/plain", nil)
		if !ok {
			continue
		}
		if code != StatusOK || string(body) != "plain" {
			t.Errorf("%s: status %d body %q", host, code, body)
		}
	}
}

// TestServeTLSRefusedOnTinyGo is the divergence that matters for safety. TinyGo's
// tls.NewListener performs no handshake, so serving through it would put
// cleartext on the TLS port; the fork must refuse instead.
func TestServeTLSRefusedOnTinyGo(t *testing.T) {
	cert, key, err := GenerateTestCertificate("localhost")
	if err != nil {
		t.Errorf("GenerateTestCertificate: %v", err)
		return
	}

	ln, _ := listenAny(t)
	if ln == nil {
		return
	}
	defer ln.Close()

	srv := &Server{Handler: testHandler}
	done := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- fmt.Errorf("panic: %v", r)
			}
		}()
		done <- srv.ServeTLSEmbed(ln, cert, key)
	}()

	if Backend == "tinygo" {
		select {
		case err := <-done:
			if err == nil {
				t.Error("ServeTLSEmbed returned nil; it must refuse rather than serve cleartext")
			} else if err != tlsServeError() {
				t.Errorf("ServeTLSEmbed: %v, want %v", err, tlsServeError())
			}
		case <-time.After(3 * time.Second):
			t.Error("ServeTLSEmbed accepted the listener; TinyGo cannot terminate TLS")
		}
		return
	}

	// Standard Go serves normally, so the only thing to check is that it did not
	// fail outright before we shut it down.
	select {
	case err := <-done:
		t.Errorf("ServeTLSEmbed returned early: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	srv.Shutdown()
}

// TestCloneTLSConfig pins the hand-written clone on the TinyGo side to standard
// Go's semantics, which are a shallow copy and nil for nil. Both are asserted
// here rather than only on TinyGo, so the day upstream changes either one, the
// two backends cannot drift apart unnoticed.
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
	// on both compilers, and fasthttp only ever sets scalar fields afterwards.
	clone.NextProtos[0] = "h2"
	if orig.NextProtos[0] != "h2" {
		t.Error("the copy is deeper than standard Go's, so the two backends differ")
	}
	if cloneTLSConfig(nil) != nil {
		t.Error("cloneTLSConfig(nil) must be nil, as (*tls.Config).Clone is")
	}
}

// TestNegotiatedProtocolAbsentOnTinyGo records that ALPN is unobservable there,
// which is why Server.NextProto -- and so HTTP/2 -- can never fire.
func TestNegotiatedProtocolAbsentOnTinyGo(t *testing.T) {
	if Backend != "tinygo" {
		t.Skip("standard Go reports the negotiated protocol")
	}
	if got := negotiatedProtocol(nil); got != "" {
		t.Errorf("negotiatedProtocol = %q, want the empty string", got)
	}
}
