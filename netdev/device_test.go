package netdev

import (
	"errors"
	"net/netip"
	"testing"
)

func loopback(port uint16) netip.AddrPort {
	return netip.AddrPortFrom(netip.AddrFrom4([4]byte{127, 0, 0, 1}), port)
}

// A bind on port 0 asks the OS to pick a port. The choice is only readable
// through getsockname, so Bind must resolve it instead of storing the request.
func TestBindPortZeroResolves(t *testing.T) {
	d := New()
	fd, err := d.Socket(AF_INET, SOCK_STREAM, IPPROTO_TCP)
	if err != nil {
		t.Fatalf("Socket: %v", err)
	}
	defer d.Close(fd)

	if err := d.Bind(fd, loopback(0)); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if err := d.Listen(fd, 1); err != nil {
		t.Fatalf("Listen: %v", err)
	}

	laddr, err := d.LocalAddr(fd)
	if err != nil {
		t.Fatalf("LocalAddr: %v", err)
	}
	if laddr.Port() == 0 {
		t.Fatalf("LocalAddr reported port 0, want the OS assignment")
	}
	if laddr.Addr() != loopback(0).Addr() {
		t.Errorf("LocalAddr = %v, want the bound 127.0.0.1", laddr)
	}
}

// Port 0 is a valid bind target but never a valid destination. Standard Go
// reports EADDRNOTAVAIL; Connect must not hand back an unconnected socket.
func TestConnectPortZeroFails(t *testing.T) {
	d := New()
	fd, err := d.Socket(AF_INET, SOCK_STREAM, IPPROTO_TCP)
	if err != nil {
		t.Fatalf("Socket: %v", err)
	}
	defer d.Close(fd)

	err = d.Connect(fd, "", loopback(0))
	if err == nil {
		t.Fatal("Connect to port 0 succeeded, want an error")
	}
	if !errors.Is(err, ErrAddrNotAvailable) {
		t.Fatalf("Connect error = %v, want ErrAddrNotAvailable", err)
	}
	if errors.Is(err, ErrConnRefused) {
		t.Error("port 0 must stay distinguishable from connection refused")
	}
}

// A closed port is a different failure from an unusable address. This also
// covers the darwin syscall-status path: a failing connect must not report nil.
func TestConnectClosedPortReportsFailure(t *testing.T) {
	d := New()

	// Bind and immediately release a port, so nothing is listening on it.
	probe, err := d.Socket(AF_INET, SOCK_STREAM, IPPROTO_TCP)
	if err != nil {
		t.Fatalf("Socket: %v", err)
	}
	if err := d.Bind(probe, loopback(0)); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	laddr, err := d.LocalAddr(probe)
	if err != nil {
		t.Fatalf("LocalAddr: %v", err)
	}
	d.Close(probe)

	fd, err := d.Socket(AF_INET, SOCK_STREAM, IPPROTO_TCP)
	if err != nil {
		t.Fatalf("Socket: %v", err)
	}
	defer d.Close(fd)

	if err := d.Connect(fd, "", laddr); err == nil {
		t.Fatalf("Connect to closed port %v succeeded, want an error", laddr)
	}
}

// An accepted connection inherits the listener's resolved local address, so a
// server started on port 0 can still report where it is.
func TestAcceptCarriesResolvedLocalAddr(t *testing.T) {
	d := New()
	ln, err := d.Socket(AF_INET, SOCK_STREAM, IPPROTO_TCP)
	if err != nil {
		t.Fatalf("Socket: %v", err)
	}
	defer d.Close(ln)
	if err := d.Bind(ln, loopback(0)); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if err := d.Listen(ln, 1); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	laddr, err := d.LocalAddr(ln)
	if err != nil {
		t.Fatalf("LocalAddr: %v", err)
	}

	accepted := make(chan int, 1)
	errc := make(chan error, 1)
	go func() {
		nfd, _, err := d.Accept(ln)
		if err != nil {
			errc <- err
			return
		}
		accepted <- nfd
	}()

	client, err := d.Socket(AF_INET, SOCK_STREAM, IPPROTO_TCP)
	if err != nil {
		t.Fatalf("Socket: %v", err)
	}
	defer d.Close(client)
	if err := d.Connect(client, "", laddr); err != nil {
		t.Fatalf("Connect to %v: %v", laddr, err)
	}

	select {
	case err := <-errc:
		t.Fatalf("Accept: %v", err)
	case nfd := <-accepted:
		defer d.Close(nfd)
		got, err := d.LocalAddr(nfd)
		if err != nil {
			t.Fatalf("LocalAddr: %v", err)
		}
		if got != laddr {
			t.Errorf("accepted LocalAddr = %v, want %v", got, laddr)
		}
	}
}

// Connect fills in the local address the OS chose for the client socket.
func TestConnectResolvesLocalAddr(t *testing.T) {
	d := New()
	ln, err := d.Socket(AF_INET, SOCK_STREAM, IPPROTO_TCP)
	if err != nil {
		t.Fatalf("Socket: %v", err)
	}
	defer d.Close(ln)
	if err := d.Bind(ln, loopback(0)); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if err := d.Listen(ln, 1); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	laddr, err := d.LocalAddr(ln)
	if err != nil {
		t.Fatalf("LocalAddr: %v", err)
	}

	client, err := d.Socket(AF_INET, SOCK_STREAM, IPPROTO_TCP)
	if err != nil {
		t.Fatalf("Socket: %v", err)
	}
	defer d.Close(client)
	if err := d.Connect(client, "", laddr); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	got, err := d.LocalAddr(client)
	if err != nil {
		t.Fatalf("LocalAddr: %v", err)
	}
	if got.Port() == 0 {
		t.Errorf("client LocalAddr = %v, want an ephemeral port", got)
	}
}

// Unix domain sockets are out of scope: the driver speaks AF_INET only.
func TestUnixDomainUnsupported(t *testing.T) {
	const afUnix = 1
	d := New()
	if _, err := d.Socket(afUnix, SOCK_STREAM, 0); !errors.Is(err, ErrFamilyNotSupported) {
		t.Fatalf("Socket(AF_UNIX) error = %v, want ErrFamilyNotSupported", err)
	}
}
