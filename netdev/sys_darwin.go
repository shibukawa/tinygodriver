//go:build darwin

package netdev

/*
#include <stdint.h>
#include <string.h>

// Socket-related syscalls are missing from TinyGo's macos-minimal-sdk libSystem
// stubs, so we invoke them via SVC. read/write/close/fcntl/select are available
// through the normal stubs and stay on libc (scheduler-safe).

// A Darwin syscall reports failure by setting the carry flag and returning the
// errno in x0; it never writes libc errno. Capturing the flag is therefore the
// only way to tell "returned 48" from "failed with EADDRINUSE", and reading
// *__error() after an svc yields a stale value from some earlier libc call.
// svc3/svc6 normalize to the libc convention: -1 plus a code in netdev_errno.
static int netdev_errno;

static long svc3(long n, long a, long b, long c) {
	register long x16 __asm__("x16") = n;
	register long x0 __asm__("x0") = a;
	register long x1 __asm__("x1") = b;
	register long x2 __asm__("x2") = c;
	long failed;
	__asm__ volatile("svc #0x80\n\tcset %[f], cs"
		: "+r"(x0), [f] "=&r"(failed)
		: "r"(x16), "r"(x1), "r"(x2)
		: "memory", "cc");
	if (failed) {
		netdev_errno = (int)x0;
		return -1;
	}
	netdev_errno = 0;
	return x0;
}

static long svc6(long n, long a, long b, long c, long d, long e, long f) {
	register long x16 __asm__("x16") = n;
	register long x0 __asm__("x0") = a;
	register long x1 __asm__("x1") = b;
	register long x2 __asm__("x2") = c;
	register long x3 __asm__("x3") = d;
	register long x4 __asm__("x4") = e;
	register long x5 __asm__("x5") = f;
	long failed;
	__asm__ volatile("svc #0x80\n\tcset %[f], cs"
		: "+r"(x0), [f] "=&r"(failed)
		: "r"(x16), "r"(x1), "r"(x2), "r"(x3), "r"(x4), "r"(x5)
		: "memory", "cc");
	if (failed) {
		netdev_errno = (int)x0;
		return -1;
	}
	netdev_errno = 0;
	return x0;
}

// Darwin arm64 / amd64 share these numbers for the calls we use.
enum {
	SYS_recvfrom    = 29,
	SYS_accept      = 30,
	SYS_getpeername = 31,
	SYS_getsockname = 32,
	SYS_socket      = 97,
	SYS_connect     = 98,
	SYS_bind        = 104,
	SYS_setsockopt  = 105,
	SYS_listen      = 106,
	SYS_sendto      = 133
};

int h_socket(int d, int t, int p) { return (int)svc3(SYS_socket, d, t, p); }
int h_bind(int fd, void *a, unsigned n) { return (int)svc3(SYS_bind, fd, (long)a, n); }
int h_listen(int fd, int b) { return (int)svc3(SYS_listen, fd, b, 0); }
int h_accept(int fd, void *a, unsigned *n) { return (int)svc3(SYS_accept, fd, (long)a, (long)n); }
int h_connect(int fd, void *a, unsigned n) { return (int)svc3(SYS_connect, fd, (long)a, n); }
int h_setsockopt(int fd, int l, int o, void *v, unsigned n) {
	return (int)svc6(SYS_setsockopt, fd, l, o, (long)v, n, 0);
}
int h_getsockname(int fd, void *a, unsigned *n) { return (int)svc3(SYS_getsockname, fd, (long)a, (long)n); }
int h_getpeername(int fd, void *a, unsigned *n) { return (int)svc3(SYS_getpeername, fd, (long)a, (long)n); }
int h_recvfrom(int fd, void *b, int n, int flags, void *from, unsigned *fromlen) {
	return (int)svc6(SYS_recvfrom, fd, (long)b, n, flags, (long)from, (long)fromlen);
}
int h_sendto(int fd, void *b, int n, int flags, void *to, unsigned tolen) {
	return (int)svc6(SYS_sendto, fd, (long)b, n, flags, (long)to, tolen);
}

long read(int fd, void *buf, unsigned long n);
long write(int fd, void *buf, unsigned long n);
int close(int fd);
int fcntl(int fd, int cmd, int arg);
int select(int nfds, void *rfds, void *wfds, void *efds, void *timeout);

// Darwin provides __error(); the linker symbol is ___error (extra underscore).
// h_errno serves the libc calls above; h_syserrno serves the svc helpers.
int *__error(void);
static int h_errno(void) { return *__error(); }
static int h_syserrno(void) { return netdev_errno; }

static int h_select(int nfds, void *rfds, void *wfds, void *efds, void *timeout) {
	return select(nfds, rfds, wfds, efds, timeout);
}
*/
import "C"
import (
	"fmt"
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
	osSOL_SOCKET    = 0xffff
	osSO_REUSEADDR  = 4
	osSO_KEEPALIVE  = 8
	osSO_LINGER     = 0x80
	osSO_NOSIGPIPE  = 0x1022
	osSOL_TCP       = 6
	osTCP_KEEPINTVL = 0x101
	osEINTR         = 4
	osEALREADY      = 37
	osEISCONN       = 56
)

type sockaddrInet4 struct {
	Len    uint8
	Family uint8
	Port   uint16
	Addr   [4]byte
	Zero   [8]byte
}

func htons(v uint16) uint16 {
	// All TinyGo host targets of interest are little-endian.
	return v<<8 | v>>8
}

func ntohs(v uint16) uint16 { return htons(v) }

func toSockaddr(ip netip.AddrPort) (sockaddrInet4, error) {
	var sa sockaddrInet4
	sa.Len = 16
	sa.Family = osAF_INET
	sa.Port = htons(ip.Port())
	if !ip.Addr().IsValid() || ip.Addr().IsUnspecified() {
		// 0.0.0.0
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

func sysErrno(code int) error {
	if code == 0 {
		// The call failed but left no code. Never report success.
		return ErrSyscall
	}
	return errnoError(code)
}

// lastErrno reports the failure of a libc call (read, write, close, select).
func lastErrno() error {
	return sysErrno(int(C.h_errno()))
}

// lastSysErrno reports the failure of a raw svc syscall. Raw syscalls never
// touch libc errno, so the code comes from the carry-flag capture in svc3/svc6.
func lastSysErrno() error {
	return sysErrno(int(C.h_syserrno()))
}

func errnoError(e int) error {
	switch e {
	case 35: // EAGAIN / EWOULDBLOCK on Darwin
		return ErrWouldBlock
	case 60: // ETIMEDOUT
		return ErrConnTimedOut
	case 61: // ECONNREFUSED
		return ErrConnRefused
	case 54: // ECONNRESET
		return ErrConnReset
	case 57: // ENOTCONN
		return ErrNotConnected
	case 48: // EADDRINUSE
		return ErrAddrInUse
	case 49: // EADDRNOTAVAIL
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
		return -1, lastSysErrno()
	}
	sysSetNoSigPipe(fd)
	return fd, nil
}

// sysSetNoSigPipe turns a write to a peer that is gone into EPIPE instead of a
// signal. Host Go installs a SIGPIPE handler that does this for every socket;
// TinyGo does not, so the default disposition kills the process. Failure is not
// worth reporting: the socket is still usable, it just stays signal-prone.
func sysSetNoSigPipe(fd int) {
	one := C.int(1)
	C.h_setsockopt(C.int(fd), osSOL_SOCKET, osSO_NOSIGPIPE, unsafe.Pointer(&one), 4)
}

func sysBind(fd int, ip netip.AddrPort) error {
	sa, err := toSockaddr(ip)
	if err != nil {
		return err
	}
	if C.h_bind(C.int(fd), unsafe.Pointer(&sa), 16) != 0 {
		return lastSysErrno()
	}
	return nil
}

func sysListen(fd, backlog int) error {
	if C.h_listen(C.int(fd), C.int(backlog)) != 0 {
		return lastSysErrno()
	}
	return nil
}

func sysAccept(fd int) (int, netip.AddrPort, error) {
	for {
		var sa sockaddrInet4
		n := C.uint(16)
		nfd := int(C.h_accept(C.int(fd), unsafe.Pointer(&sa), &n))
		if nfd >= 0 {
			sysSetNoSigPipe(nfd)
			return nfd, fromSockaddr(sa), nil
		}
		// accept blocks for the life of a server, so a signal must not end the
		// loop. EINTR only became visible once failures were detected at all.
		if int(C.h_syserrno()) == osEINTR {
			continue
		}
		return -1, netip.AddrPort{}, lastSysErrno()
	}
}

func sysConnect(fd int, ip netip.AddrPort) error {
	sa, err := toSockaddr(ip)
	if err != nil {
		return err
	}
	if C.h_connect(C.int(fd), unsafe.Pointer(&sa), 16) == 0 {
		return nil
	}
	if int(C.h_syserrno()) != osEINTR {
		return lastSysErrno()
	}
	// A signal interrupted connect. POSIX keeps the handshake going in the
	// kernel, so wait for the socket to become writable and probe with connect
	// again: EISCONN is success, EALREADY still in flight, anything else the
	// connection's real error. Under host go the runtime's preemption signal
	// makes this path common enough to see in ordinary test runs.
	for {
		var wfds fdSet
		wfds.set(fd)
		if C.h_select(C.int(fd+1), nil, unsafe.Pointer(&wfds), nil, nil) < 0 &&
			int(C.h_errno()) != osEINTR {
			return lastErrno()
		}
		if C.h_connect(C.int(fd), unsafe.Pointer(&sa), 16) == 0 {
			return nil
		}
		switch int(C.h_syserrno()) {
		case osEISCONN:
			return nil
		case osEINTR, osEALREADY:
			continue
		}
		return lastSysErrno()
	}
}

func sysClose(fd int) error {
	if C.close(C.int(fd)) != 0 {
		return lastErrno()
	}
	return nil
}

func sysSend(fd int, buf []byte, flags int) (int, error) {
	n := int(C.write(C.int(fd), unsafe.Pointer(&buf[0]), C.ulong(len(buf))))
	if n < 0 {
		return -1, lastErrno()
	}
	return n, nil
}

func sysRecv(fd int, buf []byte, flags int) (int, error) {
	n := int(C.read(C.int(fd), unsafe.Pointer(&buf[0]), C.ulong(len(buf))))
	if n < 0 {
		return -1, lastErrno()
	}
	return n, nil
}

func sysSetReuseAddr(fd int) error {
	one := C.int(1)
	if C.h_setsockopt(C.int(fd), osSOL_SOCKET, osSO_REUSEADDR, unsafe.Pointer(&one), 4) != 0 {
		return lastSysErrno()
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
			return lastSysErrno()
		}
		return nil
	case int:
		iv := C.int(v)
		if C.h_setsockopt(C.int(fd), C.int(osLevel), C.int(osOpt), unsafe.Pointer(&iv), 4) != 0 {
			return lastSysErrno()
		}
		return nil
	case float64:
		iv := C.int(v)
		if C.h_setsockopt(C.int(fd), C.int(osLevel), C.int(osOpt), unsafe.Pointer(&iv), 4) != 0 {
			return lastSysErrno()
		}
		return nil
	default:
		return ErrNotSupported
	}
}

func mapSockOpt(level, opt int) (int, int, bool) {
	// TinyGo net passes abstract constants (SOL_SOCKET=1, etc.).
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

// fd_set helpers for select (Darwin: 32-bit words, FD_SETSIZE 1024).
const fdSetSize = 1024
const fdBits = 32

type fdSet struct {
	bits [fdSetSize / fdBits]uint32
}

func (s *fdSet) set(fd int) {
	if fd < 0 || fd >= fdSetSize {
		return
	}
	s.bits[fd/fdBits] |= 1 << uint(fd%fdBits)
}

type timeval struct {
	Sec  int64
	Usec int32
	_    [4]byte // padding on arm64
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
			Usec: int32((remaining % time.Second) / time.Microsecond),
		}
		n := C.h_select(C.int(fd+1), rptr, wptr, nil, unsafe.Pointer(&tv))
		if n > 0 {
			return nil
		}
		if n == 0 {
			return ErrTimeout
		}
		// EINTR -> retry
		if int(C.h_errno()) == 4 {
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
		return netip.AddrPort{}, lastSysErrno()
	}
	return fromSockaddr(sa), nil
}

func localIPv4() (netip.Addr, error) {
	// Best effort: UDP connect to a public DNS and read the local address.
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
