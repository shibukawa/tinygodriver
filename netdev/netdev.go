// Package netdev implements a host OS Netdever for TinyGo.
//
// TinyGo's net package routes all networking through a Netdever registered via
// UseNetdev. Embedded Wi-Fi drivers fill that role on microcontrollers; this
// package fills it on Linux, macOS, and Windows so the same net/http code can
// run under TinyGo on a desktop host.
//
// Compatible with the Netdever interface described by
// https://github.com/tinygo-org/drivers/tree/dev/netdev
//
// Usage:
//
//	import _ "github.com/shibukawa/tinygodriver/netdev"
//
// The blank import registers the host driver during init.
package netdev

import (
	"errors"
	"net/netip"
	"strconv"
	"sync"
	"time"
)

// BSD-socket style constants mirrored from tinygo-org/drivers/netdev.
// Values match the abstract constants TinyGo's net package passes to drivers
// (not always the host OS numeric values).
const (
	AF_INET       = 0x2
	SOCK_STREAM   = 0x1
	SOCK_DGRAM    = 0x2
	SOL_SOCKET    = 0x1
	SO_KEEPALIVE  = 0x9
	SO_LINGER     = 0xd
	SOL_TCP       = 0x6
	TCP_KEEPINTVL = 0x5
	IPPROTO_TCP   = 0x6
	IPPROTO_UDP   = 0x11
	// Made up; TLS is implemented by the host netdev on desktop targets.
	IPPROTO_TLS = 0xFE
	F_SETFL     = 0x4
)

// Errors aligned with tinygo-org/drivers/netdev.
var (
	ErrHostUnknown          = errors.New("Host unknown")
	ErrMalAddr              = errors.New("Malformed address")
	ErrFamilyNotSupported   = errors.New("Address family not supported")
	ErrProtocolNotSupported = errors.New("Socket protocol/type not supported")
	ErrNoMoreSockets        = errors.New("No more sockets")
	ErrClosingSocket        = errors.New("Error closing socket")
	ErrNotSupported         = errors.New("Not supported")
	ErrInvalidSocketFd      = errors.New("Invalid socket fd")
	ErrTimeout              = &timeoutError{}
)

// Socket error classes shared by the three backends. Each platform maps its
// native code onto one of these, so application and test code can branch with
// errors.Is regardless of the OS. Messages match the standard Go wording.
var (
	ErrAddrNotAvailable = errors.New("can't assign requested address")
	ErrAddrInUse        = errors.New("address already in use")
	ErrConnRefused      = errors.New("connection refused")
	ErrConnReset        = errors.New("connection reset by peer")
	ErrNotConnected     = errors.New("socket is not connected")
	ErrConnTimedOut     = errors.New("connection timed out")
	ErrWouldBlock       = errors.New("resource temporarily unavailable")
	ErrSyscall          = errors.New("syscall error")

	// ErrClosed is what Accept reports once the listener has been closed. The
	// wording is standard Go's, because servers recognise a graceful shutdown by
	// matching that text: net/http compares against net.ErrClosed, and fasthttp
	// looks for the substring. Anything else reads as a crash.
	ErrClosed = errors.New("use of closed network connection")
)

type timeoutError struct{}

func (e *timeoutError) Error() string   { return "i/o timeout" }
func (e *timeoutError) Timeout() bool   { return true }
func (e *timeoutError) Temporary() bool { return true }

// syscallCodeError reports an unmapped native failure, keeping the raw code.
// Collapsing every one into a bare "syscall error" would leave a user with
// nothing to act on; errors.Is still matches ErrSyscall.
func syscallCodeError(kind string, code int) error {
	return &wrappedError{sentinel: ErrSyscall, cause: errors.New(kind + strconv.Itoa(code))}
}

// netdever matches TinyGo's net.netdev interface (and drivers/netdev.Netdever).
type netdever interface {
	GetHostByName(name string) (netip.Addr, error)
	Addr() (netip.Addr, error)
	Socket(domain int, stype int, protocol int) (int, error)
	Bind(sockfd int, ip netip.AddrPort) error
	Connect(sockfd int, host string, ip netip.AddrPort) error
	Listen(sockfd int, backlog int) error
	Accept(sockfd int) (int, netip.AddrPort, error)
	Send(sockfd int, buf []byte, flags int, deadline time.Time) (int, error)
	Recv(sockfd int, buf []byte, flags int, deadline time.Time) (int, error)
	Close(sockfd int) error
	SetSockOpt(sockfd int, level int, opt int, value interface{}) error
}

// Device is a host OS implementation of Netdever.
type Device struct {
	mu      sync.Mutex
	sockets map[int]*socket
}

type socket struct {
	// mu guards the metadata below. TLS I/O deliberately does not run under
	// it: net.Conn promises full duplex, so a Recv blocked inside the TLS
	// stack must not stall a concurrent Send. readMu serializes readers,
	// writeMu serializes writers, and Close takes both so the TLS session is
	// never torn down under an in-flight operation.
	mu      sync.Mutex
	readMu  sync.Mutex
	writeMu sync.Mutex

	fd       int
	protocol int
	isStream bool
	laddr    netip.AddrPort
	raddr    netip.AddrPort
	tls      uintptr
}

// New returns a host netdev driver. Prefer Use or the blank import.
func New() *Device {
	return &Device{
		sockets: make(map[int]*socket),
	}
}

// Use registers d as the process-wide TinyGo netdev.
func Use(d *Device) {
	if d == nil {
		d = New()
	}
	useNetdev(d)
}
