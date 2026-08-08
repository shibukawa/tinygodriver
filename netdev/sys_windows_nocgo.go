//go:build windows && !cgo

// Pure-Go socket backend for host Go on windows.
//
// Without this file, blank-importing netdev broke any `go build` on a machine
// with no C compiler: Go silently sets CGO_ENABLED=0, sys_windows.go drops out,
// and the package fails with a wall of undefined symbols. That hit programs
// that never touch netdev at all, because on host Go the blank import is a
// no-op that exists only so the same source builds under TinyGo.
//
// TinyGo cannot use this backend: it ships no windows syscall package, so
// neither syscall.Socket nor NewLazyDLL exists there. TinyGo keeps the cgo one
// and still needs mingw.
//
// syscall covers most of winsock, but Accept, Recvfrom and Sendto are EWINDOWS
// stubs in the standard library, and select is absent, so those four come
// straight from ws2_32.

package netdev

import (
	"errors"
	"net/netip"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

const (
	osAF_INET       = 2
	osSOCK_STREAM   = 1
	osSOCK_DGRAM    = 2
	osIPPROTO_TCP   = 6
	osIPPROTO_UDP   = 17
	osSOL_SOCKET    = 0xffff
	osSO_REUSEADDR  = 4
	osSO_KEEPALIVE  = 8
	osSO_LINGER     = 0x80
	osIPPROTO_TCP_L = 6
	osTCP_KEEPINTVL = 3 // not standard on Windows; best-effort
)

var (
	ws2_32             = syscall.NewLazyDLL("ws2_32.dll")
	procAccept         = ws2_32.NewProc("accept")
	procSelect         = ws2_32.NewProc("select")
	procSend           = ws2_32.NewProc("send")
	procRecv           = ws2_32.NewProc("recv")
	procWSAGetLastErr  = ws2_32.NewProc("WSAGetLastError")
	errInvalidSocketNo = errors.New("invalid socket")
)

const nocgoInvalidSocket = ^uintptr(0)

var (
	wsaOnce sync.Once
	wsaErr  error
)

func ensureWSA() error {
	wsaOnce.Do(func() {
		var d syscall.WSAData
		if err := syscall.WSAStartup(0x0202, &d); err != nil {
			wsaErr = errors.New("WSAStartup failed")
		}
	})
	return wsaErr
}

// lastErrno classifies the failure winsock recorded for the calling thread.
// The caller must have detected the failure from the return value first;
// WSAGetLastError is only consulted to name it.
func lastErrno() error {
	r, _, _ := procWSAGetLastErr.Call()
	return errnoError(int(int32(r)))
}

func errnoError(e int) error {
	switch e {
	case 10035: // WSAEWOULDBLOCK
		return ErrWouldBlock
	case 10060: // WSAETIMEDOUT
		return ErrConnTimedOut
	case 10061: // WSAECONNREFUSED
		return ErrConnRefused
	case 10054: // WSAECONNRESET
		return ErrConnReset
	case 10057: // WSAENOTCONN
		return ErrNotConnected
	case 10048: // WSAEADDRINUSE
		return ErrAddrInUse
	case 10049: // WSAEADDRNOTAVAIL
		return ErrAddrNotAvailable
	default:
		// Keep the raw code. Collapsing every unmapped failure into a bare
		// "syscall error" leaves a user with nothing to act on, and this is
		// exactly the path an unusual failure takes. errors.Is still matches
		// ErrSyscall.
		return syscallCodeError("winsock error ", e)
	}
}

// sysErr maps an error returned by the syscall package onto the shared
// classes, so errors.Is behaves the same as it does on the cgo backend.
func sysErr(err error) error {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errnoError(int(errno))
	}
	return ErrSyscall
}

// toSyscallSockaddr converts to the standard library's form, which takes the
// port in host order and does the byte swap itself.
func toSyscallSockaddr(ip netip.AddrPort) (*syscall.SockaddrInet4, error) {
	sa := &syscall.SockaddrInet4{Port: int(ip.Port())}
	if !ip.Addr().IsValid() || ip.Addr().IsUnspecified() {
		return sa, nil
	}
	if !ip.Addr().Is4() {
		if ip.Addr().Is4In6() {
			sa.Addr = ip.Addr().As4()
			return sa, nil
		}
		return nil, ErrFamilyNotSupported
	}
	sa.Addr = ip.Addr().As4()
	return sa, nil
}

func fromSyscallSockaddr(sa syscall.Sockaddr) netip.AddrPort {
	if in4, ok := sa.(*syscall.SockaddrInet4); ok {
		return netip.AddrPortFrom(netip.AddrFrom4(in4.Addr), uint16(in4.Port))
	}
	return netip.AddrPort{}
}

func sysSocket(domain, stype, proto int) (int, error) {
	if err := ensureWSA(); err != nil {
		return -1, err
	}
	var ostype, oproto int
	switch stype {
	case SOCK_STREAM:
		ostype = osSOCK_STREAM
	case SOCK_DGRAM:
		ostype = osSOCK_DGRAM
	default:
		return -1, ErrProtocolNotSupported
	}
	switch proto {
	case IPPROTO_TCP:
		oproto = osIPPROTO_TCP
	case IPPROTO_UDP:
		oproto = osIPPROTO_UDP
	case 0:
		oproto = 0
	default:
		return -1, ErrProtocolNotSupported
	}
	h, err := syscall.Socket(osAF_INET, ostype, oproto)
	if err != nil {
		return -1, sysErr(err)
	}
	return int(h), nil
}

func sysBind(fd int, ip netip.AddrPort) error {
	sa, err := toSyscallSockaddr(ip)
	if err != nil {
		return err
	}
	if err := syscall.Bind(syscall.Handle(fd), sa); err != nil {
		return sysErr(err)
	}
	return nil
}

func sysListen(fd, backlog int) error {
	if err := syscall.Listen(syscall.Handle(fd), backlog); err != nil {
		return sysErr(err)
	}
	return nil
}

// sysAccept takes the peer address from getpeername rather than from accept's
// out-parameter, so no raw sockaddr has to be decoded here.
func sysAccept(fd int) (int, netip.AddrPort, error) {
	r, _, _ := procAccept.Call(uintptr(fd), 0, 0)
	if r == nocgoInvalidSocket {
		return -1, netip.AddrPort{}, lastErrno()
	}
	sa, err := syscall.Getpeername(syscall.Handle(r))
	if err != nil {
		// The connection is up, so report it; the address is simply unknown.
		return int(r), netip.AddrPort{}, nil
	}
	return int(r), fromSyscallSockaddr(sa), nil
}

func sysConnect(fd int, ip netip.AddrPort) error {
	sa, err := toSyscallSockaddr(ip)
	if err != nil {
		return err
	}
	if err := syscall.Connect(syscall.Handle(fd), sa); err != nil {
		return sysErr(err)
	}
	return nil
}

func sysClose(fd int) error {
	if err := syscall.Closesocket(syscall.Handle(fd)); err != nil {
		return sysErr(err)
	}
	return nil
}

func sysSend(fd int, buf []byte, flags int) (int, error) {
	r, _, _ := procSend.Call(uintptr(fd), uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)), uintptr(flags))
	if int32(r) == -1 {
		return -1, lastErrno()
	}
	return int(int32(r)), nil
}

func sysRecv(fd int, buf []byte, flags int) (int, error) {
	r, _, _ := procRecv.Call(uintptr(fd), uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)), uintptr(flags))
	if int32(r) == -1 {
		return -1, lastErrno()
	}
	return int(int32(r)), nil
}

func sysSetReuseAddr(fd int) error {
	if err := syscall.SetsockoptInt(syscall.Handle(fd), osSOL_SOCKET, osSO_REUSEADDR, 1); err != nil {
		return sysErr(err)
	}
	return nil
}

func sysSetSockOpt(fd int, level, opt int, value interface{}) error {
	osLevel, osOpt, ok := mapSockOpt(level, opt)
	if !ok {
		return ErrNotSupported
	}
	var iv int
	switch v := value.(type) {
	case bool:
		if v {
			iv = 1
		}
	case int:
		iv = v
	case float64:
		iv = int(v)
	default:
		return ErrNotSupported
	}
	if err := syscall.SetsockoptInt(syscall.Handle(fd), osLevel, osOpt, iv); err != nil {
		return sysErr(err)
	}
	return nil
}

func mapSockOpt(level, opt int) (int, int, bool) {
	switch level {
	case SOL_SOCKET:
		switch opt {
		case SO_KEEPALIVE:
			return osSOL_SOCKET, osSO_KEEPALIVE, true
		case SO_LINGER:
			return osSOL_SOCKET, osSO_LINGER, true
		}
	case SOL_TCP:
		switch opt {
		case TCP_KEEPINTVL:
			return osIPPROTO_TCP_L, osTCP_KEEPINTVL, true
		}
	}
	return 0, 0, false
}

// fdSet mirrors winsock's fd_set, which is a count plus an array rather than
// the bitmask unix uses. The trailing padding Go inserts after the uint32
// matches what C does on amd64.
type fdSet struct {
	count uint32
	array [64]syscall.Handle
}

// timeval on windows uses 32-bit longs, unlike the 64-bit ones on unix.
type timeval struct {
	Sec  int32
	Usec int32
}

func waitRead(fd int, deadline time.Time) error {
	return waitFD(fd, true, deadline)
}

func waitWrite(fd int, deadline time.Time) error {
	return waitFD(fd, false, deadline)
}

func waitFD(fd int, read bool, deadline time.Time) error {
	if deadline.IsZero() {
		return nil
	}
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return ErrTimeout
		}
		var set fdSet
		set.count = 1
		set.array[0] = syscall.Handle(fd)

		tv := timeval{
			Sec:  int32(remaining / time.Second),
			Usec: int32((remaining % time.Second) / time.Microsecond),
		}
		var rptr, wptr uintptr
		if read {
			rptr = uintptr(unsafe.Pointer(&set))
		} else {
			wptr = uintptr(unsafe.Pointer(&set))
		}
		// The first argument is ignored on winsock.
		r, _, _ := procSelect.Call(0, rptr, wptr, 0, uintptr(unsafe.Pointer(&tv)))
		switch n := int32(r); {
		case n > 0:
			return nil
		case n == 0:
			return ErrTimeout
		default:
			return lastErrno()
		}
	}
}

// sysLocalAddr reads the address winsock actually assigned to fd. Bind with
// port 0 only becomes concrete here.
func sysLocalAddr(fd int) (netip.AddrPort, error) {
	sa, err := syscall.Getsockname(syscall.Handle(fd))
	if err != nil {
		return netip.AddrPort{}, sysErr(err)
	}
	return fromSyscallSockaddr(sa), nil
}

func localIPv4() (netip.Addr, error) {
	return netip.AddrFrom4([4]byte{127, 0, 0, 1}), nil
}
