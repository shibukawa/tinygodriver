//go:build windows && cgo

// The cgo socket backend. TinyGo has no windows syscall package, so this is the
// only one it can use. Host Go without a C compiler gets sys_windows_nocgo.go
// instead, which is why a plain `go build` no longer needs mingw.

package netdev

/*
#cgo LDFLAGS: -lws2_32

#include <stdint.h>

// Minimal Winsock declarations (avoid system headers under TinyGo).

typedef uint64_t SOCKET;
typedef uint16_t u_short;
typedef uint32_t u_long;

struct in_addr { u_long s_addr; };
struct sockaddr_in {
	short sin_family;
	u_short sin_port;
	struct in_addr sin_addr;
	char sin_zero[8];
};
struct timeval { long tv_sec; long tv_usec; };
struct linger { uint16_t l_onoff; uint16_t l_linger; };

typedef struct fd_set {
	unsigned int fd_count;
	SOCKET fd_array[64];
} fd_set;

int WSAStartup(uint16_t wVersionRequested, void *lpWSAData);
int WSACleanup(void);
int WSAGetLastError(void);
SOCKET socket(int af, int type, int protocol);
int bind(SOCKET s, const void *name, int namelen);
int listen(SOCKET s, int backlog);
SOCKET accept(SOCKET s, void *addr, int *addrlen);
int connect(SOCKET s, const void *name, int namelen);
int closesocket(SOCKET s);
int send(SOCKET s, const char *buf, int len, int flags);
int recv(SOCKET s, char *buf, int len, int flags);
int setsockopt(SOCKET s, int level, int optname, const char *optval, int optlen);
int getsockname(SOCKET s, void *name, int *namelen);
int select(int nfds, fd_set *readfds, fd_set *writefds, fd_set *exceptfds, struct timeval *timeout);

// INVALID_SOCKET and SOCKET_ERROR are deliberately not #defined here.
// TinyGo's cgo parses the body of every #define as a Go expression, and the
// usual ((SOCKET)(~0)) is not one, because Go has no unary ~. That made
// `tinygo build` fail to parse this file at all. The sentinels live on the Go
// side instead, as invalidSocket and socketError.

static int h_select(int nfds, fd_set *readfds, fd_set *writefds, fd_set *exceptfds, struct timeval *timeout) {
	return select(nfds, readfds, writefds, exceptfds, timeout);
}
*/
import "C"
import (
	"errors"
	"net/netip"
	"sync"
	"time"
	"unsafe"
)

// Winsock sentinels, kept in Go because they cannot be #defined above.
const (
	invalidSocket = ^C.SOCKET(0)
	socketError   = C.int(-1)
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

var wsaOnce sync.Once
var wsaErr error

func ensureWSA() error {
	wsaOnce.Do(func() {
		var data [512]byte
		if C.WSAStartup(0x0202, unsafe.Pointer(&data[0])) != 0 {
			wsaErr = errors.New("WSAStartup failed")
		}
	})
	return wsaErr
}

type sockaddrInet4 struct {
	Family uint16
	Port   uint16
	Addr   [4]byte
	Zero   [8]byte
}

func htons(v uint16) uint16 {
	return v<<8 | v>>8
}

func ntohs(v uint16) uint16 { return htons(v) }

func toSockaddr(ip netip.AddrPort) (sockaddrInet4, error) {
	var sa sockaddrInet4
	sa.Family = osAF_INET
	sa.Port = htons(ip.Port())
	if !ip.Addr().IsValid() || ip.Addr().IsUnspecified() {
		return sa, nil
	}
	if !ip.Addr().Is4() {
		if ip.Addr().Is4In6() {
			sa.Addr = ip.Addr().As4()
			return sa, nil
		}
		return sa, ErrFamilyNotSupported
	}
	sa.Addr = ip.Addr().As4()
	return sa, nil
}

func fromSockaddr(sa sockaddrInet4) netip.AddrPort {
	return netip.AddrPortFrom(netip.AddrFrom4(sa.Addr), ntohs(sa.Port))
}

// lastErrno classifies the failure winsock recorded for the calling thread.
// The caller must have detected the failure from the return value first;
// WSAGetLastError is only consulted to name it.
func lastErrno() error {
	return errnoError(int(C.WSAGetLastError()))
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
	s := C.socket(C.int(osAF_INET), C.int(ostype), C.int(oproto))
	if s == invalidSocket {
		return -1, lastErrno()
	}
	return int(s), nil
}

func sysBind(fd int, ip netip.AddrPort) error {
	sa, err := toSockaddr(ip)
	if err != nil {
		return err
	}
	if C.bind(C.SOCKET(fd), unsafe.Pointer(&sa), 16) == socketError {
		return lastErrno()
	}
	return nil
}

func sysListen(fd, backlog int) error {
	if C.listen(C.SOCKET(fd), C.int(backlog)) == socketError {
		return lastErrno()
	}
	return nil
}

func sysAccept(fd int) (int, netip.AddrPort, error) {
	var sa sockaddrInet4
	n := C.int(16)
	s := C.accept(C.SOCKET(fd), unsafe.Pointer(&sa), &n)
	if s == invalidSocket {
		return -1, netip.AddrPort{}, lastErrno()
	}
	return int(s), fromSockaddr(sa), nil
}

func sysConnect(fd int, ip netip.AddrPort) error {
	sa, err := toSockaddr(ip)
	if err != nil {
		return err
	}
	if C.connect(C.SOCKET(fd), unsafe.Pointer(&sa), 16) == socketError {
		return lastErrno()
	}
	return nil
}

func sysClose(fd int) error {
	if C.closesocket(C.SOCKET(fd)) == socketError {
		return lastErrno()
	}
	return nil
}

func sysSend(fd int, buf []byte, flags int) (int, error) {
	n := int(C.send(C.SOCKET(fd), (*C.char)(unsafe.Pointer(&buf[0])), C.int(len(buf)), C.int(flags)))
	if n == int(socketError) {
		return -1, lastErrno()
	}
	return n, nil
}

func sysRecv(fd int, buf []byte, flags int) (int, error) {
	n := int(C.recv(C.SOCKET(fd), (*C.char)(unsafe.Pointer(&buf[0])), C.int(len(buf)), C.int(flags)))
	if n == int(socketError) {
		return -1, lastErrno()
	}
	return n, nil
}

func sysSetReuseAddr(fd int) error {
	one := C.int(1)
	if C.setsockopt(C.SOCKET(fd), osSOL_SOCKET, osSO_REUSEADDR, (*C.char)(unsafe.Pointer(&one)), 4) == socketError {
		return lastErrno()
	}
	return nil
}

func sysSetSockOpt(fd int, level, opt int, value interface{}) error {
	osLevel, osOpt, ok := mapSockOpt(level, opt)
	if !ok {
		return ErrNotSupported
	}
	switch v := value.(type) {
	case bool:
		iv := C.int(0)
		if v {
			iv = 1
		}
		if C.setsockopt(C.SOCKET(fd), C.int(osLevel), C.int(osOpt), (*C.char)(unsafe.Pointer(&iv)), 4) == socketError {
			return lastErrno()
		}
		return nil
	case int:
		iv := C.int(v)
		if C.setsockopt(C.SOCKET(fd), C.int(osLevel), C.int(osOpt), (*C.char)(unsafe.Pointer(&iv)), 4) == socketError {
			return lastErrno()
		}
		return nil
	case float64:
		iv := C.int(v)
		if C.setsockopt(C.SOCKET(fd), C.int(osLevel), C.int(osOpt), (*C.char)(unsafe.Pointer(&iv)), 4) == socketError {
			return lastErrno()
		}
		return nil
	default:
		return ErrNotSupported
	}
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
		var set C.fd_set
		set.fd_count = 1
		set.fd_array[0] = C.SOCKET(fd)
		var rptr, wptr *C.fd_set
		if read {
			rptr = &set
		} else {
			wptr = &set
		}
		tv := C.struct_timeval{
			tv_sec:  C.long(remaining / time.Second),
			tv_usec: C.long((remaining % time.Second) / time.Microsecond),
		}
		n := C.h_select(0, rptr, wptr, nil, &tv)
		if n > 0 {
			return nil
		}
		if n == 0 {
			return ErrTimeout
		}
		return lastErrno()
	}
}

// sysLocalAddr reads the address winsock actually assigned to fd. Bind with
// port 0 only becomes concrete here.
func sysLocalAddr(fd int) (netip.AddrPort, error) {
	var sa sockaddrInet4
	n := C.int(16)
	if C.getsockname(C.SOCKET(fd), unsafe.Pointer(&sa), &n) == socketError {
		return netip.AddrPort{}, lastErrno()
	}
	return fromSockaddr(sa), nil
}

func localIPv4() (netip.Addr, error) {
	return netip.AddrFrom4([4]byte{127, 0, 0, 1}), nil
}
