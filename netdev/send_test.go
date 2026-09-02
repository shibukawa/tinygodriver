package netdev

import (
	"bytes"
	"testing"
	"time"
)

// TestSendWritesWholeBuffer pins io.Writer's contract at the driver: Send
// returns len(buf) or an error, never a short count with nil. A blocking
// write(2) can return short when a signal lands mid-transfer, which TinyGo's
// threads scheduler and garbage collector do routinely, and net.TCPConn.Write
// hands whatever Send returns straight to callers that trust the contract.
// Before Send looped, a 4 MiB write over loopback came back as 604596 bytes
// with no error on tinygo 0.42, and gorilla/websocket, which never reads n,
// silently dropped the tail of a frame.
//
// Under TinyGo t.Fatalf does not stop the test, so every Fatalf is followed by
// a return; see rule:tinygo-test-constraints.
func TestSendWritesWholeBuffer(t *testing.T) {
	const size = 4 << 20
	d := New()
	ln, err := d.Socket(AF_INET, SOCK_STREAM, IPPROTO_TCP)
	if err != nil {
		t.Fatalf("Socket: %v", err)
		return
	}
	defer d.Close(ln)
	if err := d.Bind(ln, loopback(0)); err != nil {
		t.Fatalf("Bind: %v", err)
		return
	}
	if err := d.Listen(ln, 1); err != nil {
		t.Fatalf("Listen: %v", err)
		return
	}
	laddr, err := d.LocalAddr(ln)
	if err != nil {
		t.Fatalf("LocalAddr: %v", err)
		return
	}

	// The peer drains everything it is sent and reports the byte count, so a
	// short Send shows up twice: in the count Send returns and in what arrived.
	type result struct {
		got []byte
		err error
	}
	drained := make(chan result, 1)
	go func() {
		nfd, _, err := d.Accept(ln)
		if err != nil {
			drained <- result{err: err}
			return
		}
		defer d.Close(nfd)
		var got bytes.Buffer
		buf := make([]byte, 64<<10)
		for got.Len() < size {
			n, err := d.Recv(nfd, buf, 0, time.Time{})
			if n > 0 {
				got.Write(buf[:n])
			}
			if err != nil {
				drained <- result{got: got.Bytes(), err: err}
				return
			}
		}
		drained <- result{got: got.Bytes()}
	}()

	client, err := d.Socket(AF_INET, SOCK_STREAM, IPPROTO_TCP)
	if err != nil {
		t.Fatalf("Socket: %v", err)
		return
	}
	defer d.Close(client)
	if err := d.Connect(client, "", laddr); err != nil {
		t.Fatalf("Connect to %v: %v", laddr, err)
		return
	}

	payload := make([]byte, size)
	for i := range payload {
		payload[i] = byte(i * 7)
	}
	n, err := d.Send(client, payload, 0, time.Time{})
	if err != nil {
		t.Fatalf("Send: n=%d err=%v", n, err)
		return
	}
	if n != size {
		t.Fatalf("Send returned %d of %d with a nil error", n, size)
		return
	}

	r := <-drained
	if r.err != nil {
		t.Fatalf("peer: got %d bytes, then %v", len(r.got), r.err)
		return
	}
	if !bytes.Equal(r.got, payload) {
		t.Fatalf("peer received %d bytes, want %d identical bytes", len(r.got), size)
		return
	}
}
