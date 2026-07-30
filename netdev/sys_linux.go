//go:build linux

package netdev

/*
#include <stdint.h>
#include <string.h>

// TinyGo's Linux linker does not expose libc socket stubs. Invoke the stable
// Linux syscall ABI directly on the two supported server architectures.
static int netdev_errno;

#if defined(__aarch64__)
static long raw_syscall6(long n, long a, long b, long c, long d, long e, long f) {
	register long x8 __asm__("x8") = n;
	register long x0 __asm__("x0") = a;
	register long x1 __asm__("x1") = b;
	register long x2 __asm__("x2") = c;
	register long x3 __asm__("x3") = d;
	register long x4 __asm__("x4") = e;
	register long x5 __asm__("x5") = f;
	__asm__ volatile("svc 0" : "+r"(x0) : "r"(x8), "r"(x1), "r"(x2), "r"(x3), "r"(x4), "r"(x5) : "memory", "cc");
	return x0;
}
enum {
	SYS_close = 57, SYS_read = 63, SYS_write = 64, SYS_pselect6 = 72,
	SYS_socket = 198, SYS_bind = 200, SYS_listen = 201, SYS_accept = 202,
	SYS_connect = 203, SYS_getsockname = 204, SYS_setsockopt = 208
};
#elif defined(__x86_64__)
static long raw_syscall6(long n, long a, long b, long c, long d, long e, long f) {
	register long r10 __asm__("r10") = d;
	register long r8 __asm__("r8") = e;
	register long r9 __asm__("r9") = f;
	long result;
	__asm__ volatile("syscall" : "=a"(result) : "a"(n), "D"(a), "S"(b), "d"(c), "r"(r10), "r"(r8), "r"(r9) : "rcx", "r11", "memory", "cc");
	return result;
}
enum {
	SYS_read = 0, SYS_write = 1, SYS_close = 3, SYS_select = 23,
	SYS_socket = 41, SYS_connect = 42, SYS_accept = 43, SYS_bind = 49,
	SYS_listen = 50, SYS_getsockname = 51, SYS_setsockopt = 54
};
#else
#error unsupported Linux architecture
#endif

static long syscall_result(long result) {
	if (result < 0 && result >= -4095) {
		netdev_errno = (int)-result;
		return -1;
	}
	netdev_errno = 0;
	return result;
}

static int h_errno(void) { return netdev_errno; }
static int h_socket(int d, int t, int p) { return (int)syscall_result(raw_syscall6(SYS_socket, d, t, p, 0, 0, 0)); }
static int h_bind(int fd, void *a, unsigned n) { return (int)syscall_result(raw_syscall6(SYS_bind, fd, (long)a, n, 0, 0, 0)); }
static int h_listen(int fd, int b) { return (int)syscall_result(raw_syscall6(SYS_listen, fd, b, 0, 0, 0, 0)); }
static int h_accept(int fd, void *a, unsigned *n) { return (int)syscall_result(raw_syscall6(SYS_accept, fd, (long)a, (long)n, 0, 0, 0)); }
static int h_connect(int fd, void *a, unsigned n) { return (int)syscall_result(raw_syscall6(SYS_connect, fd, (long)a, n, 0, 0, 0)); }
static int h_setsockopt(int fd, int l, int o, void *v, unsigned n) { return (int)syscall_result(raw_syscall6(SYS_setsockopt, fd, l, o, (long)v, n, 0)); }
static int h_getsockname(int fd, void *a, unsigned *n) { return (int)syscall_result(raw_syscall6(SYS_getsockname, fd, (long)a, (long)n, 0, 0, 0)); }
static long h_read(int fd, void *b, unsigned long n) { return syscall_result(raw_syscall6(SYS_read, fd, (long)b, n, 0, 0, 0)); }
static long h_write(int fd, void *b, unsigned long n) { return syscall_result(raw_syscall6(SYS_write, fd, (long)b, n, 0, 0, 0)); }
static int h_close(int fd) { return (int)syscall_result(raw_syscall6(SYS_close, fd, 0, 0, 0, 0, 0)); }

struct netdev_timeval { long sec; long usec; };
struct netdev_timespec { long sec; long nsec; };
static int h_select(int nfds, void *rfds, void *wfds, void *efds, void *timeout) {
#if defined(__aarch64__)
	struct netdev_timeval *tv = (struct netdev_timeval *)timeout;
	struct netdev_timespec ts;
	struct netdev_timespec *tsp = 0;
	if (tv != 0) { ts.sec = tv->sec; ts.nsec = tv->usec * 1000; tsp = &ts; }
	return (int)syscall_result(raw_syscall6(SYS_pselect6, nfds, (long)rfds, (long)wfds, (long)efds, (long)tsp, 0));
#else
	return (int)syscall_result(raw_syscall6(SYS_select, nfds, (long)rfds, (long)wfds, (long)efds, (long)timeout, 0));
#endif
}
*/
import "C"
import (
	"net/netip"
	"time"
	"unsafe"
)

const (
	osAF_INET       = 2
	osSOCK_STREAM   = 1
	osSOCK_DGRAM    = 2
	osIPPROTO_TCP   = 6
	osIPPROTO_UDP   = 17
	osSOL_SOCKET    = 1
	osSO_REUSEADDR  = 2
	osSO_KEEPALIVE  = 9
	osSO_LINGER     = 13
	osSOL_TCP       = 6
	osTCP_KEEPINTVL = 5
	osEINTR         = 4
)

// Linux sockaddr_in: sin_family is 16-bit, no length field.
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

// lastErrno reports the failure of the most recent helper call. syscall_result
// stores the code, so a failure never has to be reconstructed from libc state.
func lastErrno() error {
	e := int(C.h_errno())
	if e == 0 {
		// The call failed but left no code. Never report success.
		return ErrSyscall
	}
	return errnoError(e)
}

func errnoError(e int) error {
	switch e {
	case 11: // EAGAIN
		return ErrWouldBlock
	case 110: // ETIMEDOUT
		return ErrConnTimedOut
	case 111: // ECONNREFUSED
		return ErrConnRefused
	case 104: // ECONNRESET
		return ErrConnReset
	case 107: // ENOTCONN
		return ErrNotConnected
	case 98: // EADDRINUSE
		return ErrAddrInUse
	case 99: // EADDRNOTAVAIL
		return ErrAddrNotAvailable
	default:
		// Keep the raw code. Collapsing every unmapped failure into a bare
		// "syscall error" leaves a user with nothing to act on, and this is
		// exactly the path an unusual failure takes. errors.Is still matches
		// ErrSyscall.
		return fmt.Errorf("%w: errno %d", ErrSyscall, e)
	}
}

func sysSocket(domain, stype, proto int) (int, error) {
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
	fd := int(C.h_socket(C.int(osAF_INET), C.int(ostype), C.int(oproto)))
	if fd < 0 {
		return -1, lastErrno()
	}
	return fd, nil
}

func sysBind(fd int, ip netip.AddrPort) error {
	sa, err := toSockaddr(ip)
	if err != nil {
		return err
	}
	if C.h_bind(C.int(fd), unsafe.Pointer(&sa), 16) != 0 {
		return lastErrno()
	}
	return nil
}

func sysListen(fd, backlog int) error {
	if C.h_listen(C.int(fd), C.int(backlog)) != 0 {
		return lastErrno()
	}
	return nil
}

func sysAccept(fd int) (int, netip.AddrPort, error) {
	for {
		var sa sockaddrInet4
		n := C.uint(16)
		nfd := int(C.h_accept(C.int(fd), unsafe.Pointer(&sa), &n))
		if nfd >= 0 {
			return nfd, fromSockaddr(sa), nil
		}
		// accept blocks for the life of a server, so a signal must not end the loop.
		if int(C.h_errno()) == osEINTR {
			continue
		}
		return -1, netip.AddrPort{}, lastErrno()
	}
}

func sysConnect(fd int, ip netip.AddrPort) error {
	sa, err := toSockaddr(ip)
	if err != nil {
		return err
	}
	if C.h_connect(C.int(fd), unsafe.Pointer(&sa), 16) != 0 {
		return lastErrno()
	}
	return nil
}

func sysClose(fd int) error {
	if C.h_close(C.int(fd)) != 0 {
		return lastErrno()
	}
	return nil
}

func sysSend(fd int, buf []byte, flags int) (int, error) {
	n := int(C.h_write(C.int(fd), unsafe.Pointer(&buf[0]), C.ulong(len(buf))))
	if n < 0 {
		return -1, lastErrno()
	}
	return n, nil
}

func sysRecv(fd int, buf []byte, flags int) (int, error) {
	n := int(C.h_read(C.int(fd), unsafe.Pointer(&buf[0]), C.ulong(len(buf))))
	if n < 0 {
		return -1, lastErrno()
	}
	return n, nil
}

func sysSetReuseAddr(fd int) error {
	one := C.int(1)
	if C.h_setsockopt(C.int(fd), osSOL_SOCKET, osSO_REUSEADDR, unsafe.Pointer(&one), 4) != 0 {
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
		if C.h_setsockopt(C.int(fd), C.int(osLevel), C.int(osOpt), unsafe.Pointer(&iv), 4) != 0 {
			return lastErrno()
		}
		return nil
	case int:
		iv := C.int(v)
		if C.h_setsockopt(C.int(fd), C.int(osLevel), C.int(osOpt), unsafe.Pointer(&iv), 4) != 0 {
			return lastErrno()
		}
		return nil
	case float64:
		iv := C.int(v)
		if C.h_setsockopt(C.int(fd), C.int(osLevel), C.int(osOpt), unsafe.Pointer(&iv), 4) != 0 {
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
			return osSOL_TCP, osTCP_KEEPINTVL, true
		}
	}
	return 0, 0, false
}

const fdSetSize = 1024
const fdBits = 64 // Linux __NFDBITS is typically 64 on 64-bit

type fdSet struct {
	bits [fdSetSize / fdBits]uint64
}

func (s *fdSet) set(fd int) {
	if fd < 0 || fd >= fdSetSize {
		return
	}
	s.bits[fd/fdBits] |= 1 << uint(fd%fdBits)
}

type timeval struct {
	Sec  int64
	Usec int64
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
		var rfds, wfds fdSet
		var rptr, wptr unsafe.Pointer
		if read {
			rfds.set(fd)
			rptr = unsafe.Pointer(&rfds)
		} else {
			wfds.set(fd)
			wptr = unsafe.Pointer(&wfds)
		}
		tv := timeval{
			Sec:  int64(remaining / time.Second),
			Usec: int64((remaining % time.Second) / time.Microsecond),
		}
		n := C.h_select(C.int(fd+1), rptr, wptr, nil, unsafe.Pointer(&tv))
		if n > 0 {
			return nil
		}
		if n == 0 {
			return ErrTimeout
		}
		if int(C.h_errno()) == osEINTR {
			continue
		}
		return lastErrno()
	}
}

// sysLocalAddr reads the address the kernel actually assigned to fd. Bind with
// port 0 only becomes concrete here.
func sysLocalAddr(fd int) (netip.AddrPort, error) {
	var sa sockaddrInet4
	n := C.uint(16)
	if C.h_getsockname(C.int(fd), unsafe.Pointer(&sa), &n) != 0 {
		return netip.AddrPort{}, lastErrno()
	}
	return fromSockaddr(sa), nil
}

func localIPv4() (netip.Addr, error) {
	fd, err := sysSocket(AF_INET, SOCK_DGRAM, IPPROTO_UDP)
	if err != nil {
		return netip.AddrFrom4([4]byte{127, 0, 0, 1}), nil
	}
	defer sysClose(fd)
	_ = sysConnect(fd, netip.AddrPortFrom(netip.AddrFrom4([4]byte{8, 8, 8, 8}), 53))
	var sa sockaddrInet4
	n := C.uint(16)
	if C.h_getsockname(C.int(fd), unsafe.Pointer(&sa), &n) != 0 {
		return netip.AddrFrom4([4]byte{127, 0, 0, 1}), nil
	}
	return fromSockaddr(sa).Addr(), nil
}
