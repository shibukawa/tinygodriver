package websocket_test

// End-to-end checks that the fork serves and dials real sockets under both
// compilers. The upstream suite in this package already covers framing over
// net.Pipe; what it cannot cover is the pairing this repository adds, where the
// server side reaches Upgrade through httpserver because net/http's own Hijack
// deadlocks on netdev.
//
// Two TinyGo facts shape the helpers: net.Listener.Addr() reports port 0 for a
// port 0 listen, so the port is chosen here; and t.Fatalf does not stop the
// goroutine, so every failure is Errorf followed by an explicit return.

import (
	"bytes"
	"context"
	"math/rand"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shibukawa/tinygodriver/httpserver"
	_ "github.com/shibukawa/tinygodriver/netdev"
	"github.com/shibukawa/tinygodriver/websocket"
)

var nextPort int32 = 19800

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

// echoServer serves /echo, /echoz (compressed), /stream (never buffers a whole
// message), /sub, /deny, /limit and /closer.
func echoServer(t *testing.T) http.Handler {
	t.Helper()
	allow := func(r *http.Request) bool { return true }
	mux := http.NewServeMux()

	echo := func(compress bool) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			u := websocket.Upgrader{
				ReadBufferSize:    1024,
				WriteBufferSize:   1024,
				EnableCompression: compress,
				CheckOrigin:       allow,
			}
			c, err := u.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer c.Close()
			c.SetReadLimit(32 << 20)
			if compress {
				c.EnableWriteCompression(true)
			}
			for {
				mt, msg, err := c.ReadMessage()
				if err != nil {
					return
				}
				if err := c.WriteMessage(mt, msg); err != nil {
					return
				}
			}
		}
	}
	mux.HandleFunc("/echo", echo(false))
	mux.HandleFunc("/echoz", echo(true))

	mux.HandleFunc("/stream", func(w http.ResponseWriter, r *http.Request) {
		u := websocket.Upgrader{CheckOrigin: allow}
		c, err := u.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		c.SetReadLimit(64 << 20)
		for {
			mt, rd, err := c.NextReader()
			if err != nil {
				return
			}
			wr, err := c.NextWriter(mt)
			if err != nil {
				return
			}
			buf := make([]byte, 7919) // odd size, so the copy straddles frames
			for {
				n, rerr := rd.Read(buf)
				if n > 0 {
					if _, werr := wr.Write(buf[:n]); werr != nil {
						return
					}
				}
				if rerr != nil {
					break
				}
			}
			if err := wr.Close(); err != nil {
				return
			}
		}
	})

	mux.HandleFunc("/sub", func(w http.ResponseWriter, r *http.Request) {
		u := websocket.Upgrader{
			Subprotocols: []string{"chat.v2", "chat.v1"},
			CheckOrigin:  allow,
		}
		c, err := u.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		_ = c.WriteMessage(websocket.TextMessage, []byte("picked:"+c.Subprotocol()))
		time.Sleep(200 * time.Millisecond)
	})

	mux.HandleFunc("/deny", func(w http.ResponseWriter, r *http.Request) {
		u := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return false }}
		_, _ = u.Upgrade(w, r, nil)
	})

	mux.HandleFunc("/limit", func(w http.ResponseWriter, r *http.Request) {
		u := websocket.Upgrader{CheckOrigin: allow}
		c, err := u.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		c.SetReadLimit(64)
		for {
			if _, _, err := c.ReadMessage(); err != nil {
				return
			}
		}
	})

	mux.HandleFunc("/closer", func(w http.ResponseWriter, r *http.Request) {
		u := websocket.Upgrader{CheckOrigin: allow}
		c, err := u.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		_, _, _ = c.ReadMessage()
		_ = c.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(4000, "bye-4000"),
			time.Now().Add(2*time.Second))
		time.Sleep(100 * time.Millisecond)
	})

	mux.HandleFunc("/pinger", func(w http.ResponseWriter, r *http.Request) {
		u := websocket.Upgrader{CheckOrigin: allow}
		c, err := u.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		_ = c.WriteControl(websocket.PingMessage, []byte("srv-ping"),
			time.Now().Add(2*time.Second))
		for {
			mt, msg, err := c.ReadMessage()
			if err != nil {
				return
			}
			if err := c.WriteMessage(mt, msg); err != nil {
				return
			}
		}
	})

	mux.HandleFunc("/plain", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not a websocket"))
	})
	return mux
}

// start serves echoServer and returns its address.
func start(t *testing.T) (string, bool) {
	t.Helper()
	ln, addr := listenSomewhere(t)
	if ln == nil {
		return "", false
	}
	srv := &http.Server{Handler: echoServer(t)}
	go func() { _ = httpserver.Serve(ln, srv) }()
	return addr, true
}

func dialWS(t *testing.T, addr, path string) *websocket.Conn {
	t.Helper()
	d := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	c, _, err := d.Dial("ws://"+addr+path, nil)
	if err != nil {
		t.Errorf("dial %s: %v", path, err)
		return nil
	}
	return c
}

// roundTrip sends one message and returns the echo.
func roundTrip(c *websocket.Conn, mt int, payload []byte) ([]byte, error) {
	if err := c.WriteMessage(mt, payload); err != nil {
		return nil, err
	}
	_ = c.SetReadDeadline(time.Now().Add(20 * time.Second))
	_, got, err := c.ReadMessage()
	return got, err
}

func TestEcho(t *testing.T) {
	addr, ok := start(t)
	if !ok {
		return
	}
	c := dialWS(t, addr, "/echo")
	if c == nil {
		return
	}
	defer c.Close()

	cases := []struct {
		name    string
		mt      int
		payload []byte
	}{
		{"text", websocket.TextMessage, []byte("hello, 世界")},
		{"binary", websocket.BinaryMessage, []byte{0x00, 0xff, 0x7f, 0x80, 0x01}},
		{"empty", websocket.TextMessage, []byte{}},
	}
	for _, tc := range cases {
		got, err := roundTrip(c, tc.mt, tc.payload)
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			return
		}
		if !bytes.Equal(got, tc.payload) {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.payload)
		}
	}
}

// TestMaskLengths drives every branch of maskBytes: the small-buffer path, the
// alignment prologue, the uintptr word loop and the tail. It is the check that
// matters most under TinyGo, where that unsafe arithmetic is compiled by LLVM.
func TestMaskLengths(t *testing.T) {
	addr, ok := start(t)
	if !ok {
		return
	}
	c := dialWS(t, addr, "/echo")
	if c == nil {
		return
	}
	defer c.Close()

	rnd := rand.New(rand.NewSource(12345))
	for n := 0; n <= 600; n++ {
		payload := make([]byte, n)
		rnd.Read(payload)
		got, err := roundTrip(c, websocket.BinaryMessage, payload)
		if err != nil {
			t.Errorf("length %d: %v", n, err)
			return
		}
		if !bytes.Equal(got, payload) {
			t.Errorf("length %d: payload did not survive masking", n)
			return
		}
	}

	// Frame-header boundaries: 7-bit, 16-bit and 64-bit length encodings.
	for _, n := range []int{125, 126, 127, 255, 256, 65534, 65535, 65536, 65537} {
		payload := make([]byte, n)
		rnd.Read(payload)
		got, err := roundTrip(c, websocket.BinaryMessage, payload)
		if err != nil {
			t.Errorf("boundary %d: %v", n, err)
			return
		}
		if !bytes.Equal(got, payload) {
			t.Errorf("boundary %d: payload did not survive masking", n)
			return
		}
	}

	// A deliberately unaligned slice, so the alignment prologue runs.
	base := make([]byte, 4096+8)
	rnd.Read(base)
	unaligned := base[3 : 3+4096]
	got, err := roundTrip(c, websocket.BinaryMessage, unaligned)
	if err != nil {
		t.Errorf("unaligned: %v", err)
		return
	}
	if !bytes.Equal(got, unaligned) {
		t.Errorf("unaligned payload did not survive masking")
	}
}

func TestFragmentedWrite(t *testing.T) {
	addr, ok := start(t)
	if !ok {
		return
	}
	c := dialWS(t, addr, "/echo")
	if c == nil {
		return
	}
	defer c.Close()

	wr, err := c.NextWriter(websocket.BinaryMessage)
	if err != nil {
		t.Errorf("NextWriter: %v", err)
		return
	}
	var want bytes.Buffer
	for i := 0; i < 1000; i++ {
		chunk := []byte("chunk-" + strconv.Itoa(i) + ";")
		want.Write(chunk)
		if _, err := wr.Write(chunk); err != nil {
			t.Errorf("write chunk %d: %v", i, err)
			return
		}
	}
	if err := wr.Close(); err != nil {
		t.Errorf("close writer: %v", err)
		return
	}
	_ = c.SetReadDeadline(time.Now().Add(20 * time.Second))
	_, got, err := c.ReadMessage()
	if err != nil {
		t.Errorf("read: %v", err)
		return
	}
	if !bytes.Equal(got, want.Bytes()) {
		t.Errorf("got %d bytes, want %d", len(got), want.Len())
	}
}

// TestLargeMessage sends more than both socket buffers hold. The write runs on
// its own goroutine: writing it all before reading would deadlock against the
// echo, which is a property of the test, not of the library.
func TestLargeMessage(t *testing.T) {
	addr, ok := start(t)
	if !ok {
		return
	}
	c := dialWS(t, addr, "/stream")
	if c == nil {
		return
	}
	defer c.Close()

	big := make([]byte, 4<<20)
	rand.New(rand.NewSource(7)).Read(big)
	werr := make(chan error, 1)
	go func() { werr <- c.WriteMessage(websocket.BinaryMessage, big) }()

	_ = c.SetReadDeadline(time.Now().Add(60 * time.Second))
	_, rd, err := c.NextReader()
	if err != nil {
		t.Errorf("NextReader: %v", err)
		return
	}
	var acc bytes.Buffer
	buf := make([]byte, 7919)
	reads := 0
	for {
		n, rerr := rd.Read(buf)
		acc.Write(buf[:n])
		reads++
		if rerr != nil {
			break
		}
	}
	if err := <-werr; err != nil {
		t.Errorf("write: %v", err)
		return
	}
	if !bytes.Equal(acc.Bytes(), big) {
		t.Errorf("got %d bytes, want %d", acc.Len(), len(big))
	}
	if reads < 2 {
		t.Errorf("read the message in %d call(s); the streaming path never ran", reads)
	}
}

func TestPingPong(t *testing.T) {
	addr, ok := start(t)
	if !ok {
		return
	}
	c := dialWS(t, addr, "/pinger")
	if c == nil {
		return
	}
	defer c.Close()

	var seenPing, seenPong atomic.Value
	seenPing.Store("")
	seenPong.Store("")
	c.SetPingHandler(func(data string) error {
		seenPing.Store(data)
		return c.WriteControl(websocket.PongMessage, []byte(data), time.Now().Add(2*time.Second))
	})
	c.SetPongHandler(func(data string) error {
		seenPong.Store(data)
		return nil
	})

	// Any read drives the control-frame machinery, delivering the ping the
	// server sent on connect.
	if _, err := roundTrip(c, websocket.TextMessage, []byte("drive")); err != nil {
		t.Errorf("drive read: %v", err)
		return
	}
	if got := seenPing.Load().(string); got != "srv-ping" {
		t.Errorf("ping payload %q, want %q", got, "srv-ping")
	}

	if err := c.WriteControl(websocket.PingMessage, []byte("cli-ping"), time.Now().Add(2*time.Second)); err != nil {
		t.Errorf("WriteControl ping: %v", err)
		return
	}
	if _, err := roundTrip(c, websocket.TextMessage, []byte("drive2")); err != nil {
		t.Errorf("second drive read: %v", err)
		return
	}
	if got := seenPong.Load().(string); got != "cli-ping" {
		t.Errorf("pong payload %q, want %q", got, "cli-ping")
	}
}

func TestCloseHandshake(t *testing.T) {
	addr, ok := start(t)
	if !ok {
		return
	}
	c := dialWS(t, addr, "/closer")
	if c == nil {
		return
	}
	defer c.Close()

	_ = c.WriteMessage(websocket.TextMessage, []byte("go"))
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, _, err := c.ReadMessage()
	if !websocket.IsCloseError(err, 4000) {
		t.Errorf("got %v, want close 4000", err)
		return
	}
	if websocket.IsUnexpectedCloseError(err, 4000) {
		t.Errorf("4000 was listed as expected, yet reported unexpected")
	}
	if !websocket.IsUnexpectedCloseError(err) {
		t.Errorf("with nothing expected, 4000 should be unexpected")
	}
}

func TestReadLimit(t *testing.T) {
	addr, ok := start(t)
	if !ok {
		return
	}
	c := dialWS(t, addr, "/limit")
	if c == nil {
		return
	}
	defer c.Close()

	_ = c.WriteMessage(websocket.BinaryMessage, make([]byte, 4096))
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, _, err := c.ReadMessage()
	if !websocket.IsCloseError(err, websocket.CloseMessageTooBig) {
		t.Errorf("got %v, want close 1009", err)
	}
}

// countingConn measures what actually reaches the socket, so compression can be
// shown to do something rather than merely be negotiated.
type countingConn struct {
	net.Conn
	wrote int64
}

func (c *countingConn) Write(b []byte) (int, error) {
	n, err := c.Conn.Write(b)
	atomic.AddInt64(&c.wrote, int64(n))
	return n, err
}

func TestCompression(t *testing.T) {
	addr, ok := start(t)
	if !ok {
		return
	}
	var counted *countingConn
	d := websocket.Dialer{
		HandshakeTimeout:  10 * time.Second,
		EnableCompression: true,
		NetDialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			raw, err := net.Dial(network, address)
			if err != nil {
				return nil, err
			}
			counted = &countingConn{Conn: raw}
			return counted, nil
		},
	}
	c, resp, err := d.Dial("ws://"+addr+"/echoz", nil)
	if err != nil {
		t.Errorf("dial: %v", err)
		return
	}
	defer c.Close()
	if ext := resp.Header.Get("Sec-Websocket-Extensions"); !strings.Contains(ext, "permessage-deflate") {
		t.Errorf("extensions %q, want permessage-deflate", ext)
		return
	}

	payload := bytes.Repeat([]byte("the quick brown fox jumps over the lazy dog. "), 2000)
	before := atomic.LoadInt64(&counted.wrote)
	got, err := roundTrip(c, websocket.TextMessage, payload)
	wire := atomic.LoadInt64(&counted.wrote) - before
	if err != nil {
		t.Errorf("round trip: %v", err)
		return
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("compressed round trip corrupted the payload")
		return
	}
	if wire == 0 || wire >= int64(len(payload))/8 {
		t.Errorf("wrote %d bytes for a %d byte payload; compression did not engage",
			wire, len(payload))
	}
}

func TestSubprotocol(t *testing.T) {
	addr, ok := start(t)
	if !ok {
		return
	}
	d := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
		Subprotocols:     []string{"chat.v1", "chat.v9"},
	}
	c, _, err := d.Dial("ws://"+addr+"/sub", nil)
	if err != nil {
		t.Errorf("dial: %v", err)
		return
	}
	defer c.Close()
	if c.Subprotocol() != "chat.v1" {
		t.Errorf("client negotiated %q, want chat.v1", c.Subprotocol())
	}
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, msg, err := c.ReadMessage()
	if err != nil {
		t.Errorf("read: %v", err)
		return
	}
	if string(msg) != "picked:chat.v1" {
		t.Errorf("server reported %q, want picked:chat.v1", msg)
	}
}

func TestHandshakeFailures(t *testing.T) {
	addr, ok := start(t)
	if !ok {
		return
	}
	d := websocket.Dialer{HandshakeTimeout: 10 * time.Second}

	_, resp, err := d.Dial("ws://"+addr+"/deny", http.Header{"Origin": {"http://evil.example"}})
	if err != websocket.ErrBadHandshake {
		t.Errorf("origin check: got %v, want ErrBadHandshake", err)
	} else if resp.StatusCode != http.StatusForbidden {
		t.Errorf("origin check: status %d, want 403", resp.StatusCode)
	}

	_, _, err = d.Dial("ws://"+addr+"/plain", nil)
	if err != websocket.ErrBadHandshake {
		t.Errorf("plain endpoint: got %v, want ErrBadHandshake", err)
	}
}

// TestWSSNeedsDialer pins the TinyGo TLS contract: no panic, a refusal the
// caller can act on. Under standard Go the same dial reaches a real handshake
// and fails against a plaintext listener, which is equally not a panic.
func TestWSSNeedsDialer(t *testing.T) {
	addr, ok := start(t)
	if !ok {
		return
	}
	d := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	_, _, err := d.Dial("wss://"+addr+"/echo", nil)
	if err == nil {
		t.Errorf("wss to a plaintext listener succeeded")
		return
	}
	if websocket.Backend == "tinygo" && err != websocket.ErrTLSUnsupported {
		t.Errorf("got %v, want ErrTLSUnsupported", err)
	}
}

func TestConcurrentClients(t *testing.T) {
	addr, ok := start(t)
	if !ok {
		return
	}
	const clients, msgs = 16, 50
	var wg sync.WaitGroup
	var failures, delivered int64
	for i := 0; i < clients; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			d := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
			c, _, err := d.Dial("ws://"+addr+"/echo", nil)
			if err != nil {
				atomic.AddInt64(&failures, 1)
				return
			}
			defer c.Close()
			for j := 0; j < msgs; j++ {
				payload := []byte("client-" + strconv.Itoa(id) + "-msg-" + strconv.Itoa(j))
				got, err := roundTrip(c, websocket.TextMessage, payload)
				if err != nil || !bytes.Equal(got, payload) {
					atomic.AddInt64(&failures, 1)
					return
				}
				atomic.AddInt64(&delivered, 1)
			}
		}(i)
	}
	wg.Wait()
	if failures != 0 || delivered != clients*msgs {
		t.Errorf("delivered %d of %d with %d failures",
			delivered, clients*msgs, failures)
	}
}
