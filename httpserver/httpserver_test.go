package httpserver_test

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shibukawa/tinygodriver/httpserver"
	_ "github.com/shibukawa/tinygodriver/netdev"
)

// Two TinyGo facts shape every test below.
//
// net.Listener.Addr() reports port 0 for a port 0 listen under netdev, so a
// test cannot ask the listener which port it got. listenSomewhere picks the
// port itself and returns the address it chose.
//
// t.Fatalf does not stop the goroutine: TinyGo's testing package prints
// "FailNow is incomplete, requires runtime.Goexit()" and carries on. Every
// Fatalf here is therefore followed by an explicit return, and helpers report
// failure through a second result rather than by not returning.

var nextPort int32 = 19700

func listenSomewhere(t *testing.T) (net.Listener, string) {
	t.Helper()
	for i := 0; i < 64; i++ {
		addr := "127.0.0.1:" + strconv.Itoa(int(atomic.AddInt32(&nextPort, 1)))
		ln, err := net.Listen("tcp", addr)
		if err == nil {
			return ln, addr
		}
	}
	t.Errorf("no free port in range")
	return nil, ""
}

// upgradeEcho is the smallest handler that hijacks: it answers the handshake
// itself, then echoes lines. A hand-written upgrade rather than a WebSocket
// library keeps this package's tests free of that dependency.
func upgradeEcho(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h, ok := w.(http.Hijacker)
		if !ok {
			t.Errorf("ResponseWriter is not a Hijacker")
			http.Error(w, "no hijacker", http.StatusInternalServerError)
			return
		}
		conn, brw, err := h.Hijack()
		if err != nil {
			t.Errorf("Hijack: %v", err)
			return
		}
		defer conn.Close()
		if _, err := conn.Write([]byte("HTTP/1.1 101 Switching Protocols\r\nUpgrade: echo\r\nConnection: Upgrade\r\n\r\n")); err != nil {
			return
		}
		for {
			line, err := brw.Reader.ReadString('\n')
			if err != nil {
				return
			}
			if _, err := conn.Write([]byte("echo:" + line)); err != nil {
				return
			}
		}
	}
}

func testMux(t *testing.T) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/plain", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("plain body"))
	})
	mux.HandleFunc("/refuse", func(w http.ResponseWriter, r *http.Request) {
		// An upgrade handler that changes its mind: it must produce a normal
		// response through the bypass writer.
		http.Error(w, "nope", http.StatusForbidden)
	})
	mux.HandleFunc("/up", upgradeEcho(t))
	return mux
}

// start serves testMux and returns the address, or ok=false having reported.
func start(t *testing.T, cfg httpserver.Config) (addr string, ok bool) {
	t.Helper()
	ln, addr := listenSomewhere(t)
	if ln == nil {
		return "", false
	}
	srv := &http.Server{Handler: testMux(t)}
	go func() {
		_ = httpserver.ServeConfig(ln, srv, cfg)
	}()
	return addr, true
}

func dial(t *testing.T, addr string) net.Conn {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Errorf("dial %s: %v", addr, err)
		return nil
	}
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	return c
}

// request writes one request and reads one response. The reference request is
// non-nil because TinyGo's ReadResponse dereferences it even though its own doc
// says nil is allowed.
func request(t *testing.T, c net.Conn, br *bufio.Reader, addr, path string) *http.Response {
	t.Helper()
	if _, err := fmt.Fprintf(c, "GET %s HTTP/1.1\r\nHost: %s\r\n\r\n", path, addr); err != nil {
		t.Errorf("write request: %v", err)
		return nil
	}
	ref, err := http.NewRequest("GET", "http://"+addr+path, nil)
	if err != nil {
		t.Errorf("NewRequest: %v", err)
		return nil
	}
	resp, err := http.ReadResponse(br, ref)
	if err != nil {
		t.Errorf("ReadResponse: %v", err)
		return nil
	}
	return resp
}

func TestOrdinaryRequest(t *testing.T) {
	addr, ok := start(t, httpserver.Config{})
	if !ok {
		return
	}
	c := dial(t, addr)
	if c == nil {
		return
	}
	defer c.Close()

	resp := request(t, c, bufio.NewReader(c), addr, "/plain")
	if resp == nil {
		return
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Errorf("read body: %v", err)
		return
	}
	if resp.StatusCode != 200 || string(body) != "plain body" {
		t.Errorf("got %d %q, want 200 %q", resp.StatusCode, body, "plain body")
	}
}

// TestKeepAlive is the check that distinguishes this package from simply
// replacing http.Server: ordinary endpoints must still reuse a connection.
func TestKeepAlive(t *testing.T) {
	addr, ok := start(t, httpserver.Config{})
	if !ok {
		return
	}
	c := dial(t, addr)
	if c == nil {
		return
	}
	defer c.Close()
	br := bufio.NewReader(c)

	for i := 0; i < 3; i++ {
		resp := request(t, c, br, addr, "/plain")
		if resp == nil {
			return
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Errorf("request %d: read body: %v", i, err)
			return
		}
		if resp.StatusCode != 200 || string(body) != "plain body" {
			t.Errorf("request %d: got %d %q", i, resp.StatusCode, body)
			return
		}
		if resp.Close {
			t.Errorf("request %d: server announced close, connection not reusable", i)
			return
		}
	}
}

// readHandshake consumes a status line and headers, returning the status line.
func readHandshake(t *testing.T, br *bufio.Reader) (string, bool) {
	t.Helper()
	status, err := br.ReadString('\n')
	if err != nil {
		t.Errorf("read status: %v", err)
		return "", false
	}
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Errorf("read header: %v", err)
			return "", false
		}
		if line == "\r\n" {
			return status, true
		}
	}
}

func TestUpgradeHijacks(t *testing.T) {
	addr, ok := start(t, httpserver.Config{})
	if !ok {
		return
	}
	c := dial(t, addr)
	if c == nil {
		return
	}
	defer c.Close()

	fmt.Fprintf(c, "GET /up HTTP/1.1\r\nHost: %s\r\nUpgrade: echo\r\nConnection: Upgrade\r\n\r\n", addr)
	br := bufio.NewReader(c)
	status, ok := readHandshake(t, br)
	if !ok {
		return
	}
	if !strings.HasPrefix(status, "HTTP/1.1 101 ") {
		t.Errorf("got status %q, want 101", strings.TrimSpace(status))
		return
	}
	// The connection now speaks the upgraded protocol.
	if _, err := c.Write([]byte("hello\n")); err != nil {
		t.Errorf("write: %v", err)
		return
	}
	got, err := br.ReadString('\n')
	if err != nil {
		t.Errorf("read echo: %v", err)
		return
	}
	if got != "echo:hello\n" {
		t.Errorf("got %q, want %q", got, "echo:hello\n")
	}
}

// TestUpgradeHandlerMayDecline covers the bypass writer's ordinary response
// path: a handler reached through the bypass that answers normally instead.
func TestUpgradeHandlerMayDecline(t *testing.T) {
	addr, ok := start(t, httpserver.Config{})
	if !ok {
		return
	}
	c := dial(t, addr)
	if c == nil {
		return
	}
	defer c.Close()

	fmt.Fprintf(c, "GET /refuse HTTP/1.1\r\nHost: %s\r\nUpgrade: echo\r\nConnection: Upgrade\r\n\r\n", addr)
	ref, err := http.NewRequest("GET", "http://"+addr+"/refuse", nil)
	if err != nil {
		t.Errorf("NewRequest: %v", err)
		return
	}
	resp, err := http.ReadResponse(bufio.NewReader(c), ref)
	if err != nil {
		t.Errorf("ReadResponse: %v", err)
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("got %d, want 403", resp.StatusCode)
		return
	}
	if strings.TrimSpace(string(body)) != "nope" {
		t.Errorf("got body %q, want %q", body, "nope")
	}
	if resp.ContentLength != int64(len(body)) {
		t.Errorf("Content-Length %d, body %d: length must be accurate",
			resp.ContentLength, len(body))
	}
}

// TestUpgradeOnReusedConnection pins the documented limit. Under standard Go
// net/http hijacks happily, so the upgrade succeeds; on the TinyGo path it must
// answer 501 rather than hang.
func TestUpgradeOnReusedConnection(t *testing.T) {
	addr, ok := start(t, httpserver.Config{})
	if !ok {
		return
	}
	c := dial(t, addr)
	if c == nil {
		return
	}
	defer c.Close()
	br := bufio.NewReader(c)

	resp := request(t, c, br, addr, "/plain")
	if resp == nil {
		return
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	fmt.Fprintf(c, "GET /up HTTP/1.1\r\nHost: %s\r\nUpgrade: echo\r\nConnection: Upgrade\r\n\r\n", addr)
	status, err := br.ReadString('\n')
	if err != nil {
		t.Errorf("second request: %v", err)
		return
	}
	want := "HTTP/1.1 501 "
	if httpserver.Backend == "std" {
		want = "HTTP/1.1 101 "
	}
	if !strings.HasPrefix(status, want) {
		t.Errorf("%s path: got %q, want %s", httpserver.Backend, strings.TrimSpace(status), want)
	}
}

// TestReadHeaderTimeout proves a client that stalls mid-header is dropped
// rather than holding a goroutine forever.
func TestReadHeaderTimeout(t *testing.T) {
	addr, ok := start(t, httpserver.Config{ReadHeaderTimeout: 300 * time.Millisecond})
	if !ok {
		return
	}
	c := dial(t, addr)
	if c == nil {
		return
	}
	defer c.Close()

	// A head that never ends.
	if _, err := fmt.Fprintf(c, "GET /plain HTTP/1.1\r\nHost: %s\r\n", addr); err != nil {
		t.Errorf("write: %v", err)
		return
	}
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	began := time.Now()
	_, err := io.ReadAll(c)
	elapsed := time.Since(began)
	if ne, isNet := err.(net.Error); isNet && ne.Timeout() {
		t.Errorf("connection still open after %v: the head read is unbounded", elapsed)
		return
	}
	if elapsed > 3*time.Second {
		t.Errorf("took %v to drop a stalled client", elapsed)
	}
}

func TestIsUpgrade(t *testing.T) {
	cases := []struct {
		connection string
		want       bool
	}{
		{"Upgrade", true},
		{"upgrade", true},
		{"keep-alive, Upgrade", true},
		{"Upgrade, keep-alive", true},
		{" UPGRADE ", true},
		{"keep-alive", false},
		{"", false},
		{"upgraded", false},
	}
	for _, tc := range cases {
		r, err := http.NewRequest("GET", "http://example.test/", nil)
		if err != nil {
			t.Errorf("NewRequest: %v", err)
			return
		}
		if tc.connection != "" {
			r.Header.Set("Connection", tc.connection)
		}
		if got := httpserver.IsUpgrade(r); got != tc.want {
			t.Errorf("IsUpgrade(Connection: %q) = %v, want %v", tc.connection, got, tc.want)
		}
	}
}

func TestNilServer(t *testing.T) {
	ln, _ := listenSomewhere(t)
	if ln == nil {
		return
	}
	defer ln.Close()
	if err := httpserver.Serve(ln, nil); !errors.Is(err, httpserver.ErrNilServer) {
		t.Errorf("got %v, want ErrNilServer", err)
	}
}
