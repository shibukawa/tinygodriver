//go:build wasip1 || wasip2

package netdev

// WASI has no host socket backend here yet. Preview 1 has no outbound sockets
// at all, and the preview 2 wasi-sockets backend is not implemented. These
// stubs exist so a wasip1/wasip2 build fails at the first network call with a
// reason, instead of failing at link time with undefined symbols. Note that
// TinyGo's wasip2 target reports GOOS=linux, so the linux files carry !wasip2
// and this file matches the wasip2 build tag instead.

import (
	"errors"
	"net/netip"
	"time"
)

var errNoNetwork = &wrappedError{
	sentinel: ErrNotSupported,
	cause:    errors.New("no socket backend on WASI (wasip1 has no outbound sockets; the wasip2 wasi-sockets backend is not implemented)"),
}

func sysSocket(domain, stype, proto int) (int, error) { return -1, errNoNetwork }

func sysBind(fd int, ip netip.AddrPort) error { return errNoNetwork }

func sysListen(fd, backlog int) error { return errNoNetwork }

func sysAccept(fd int) (int, netip.AddrPort, error) {
	return -1, netip.AddrPort{}, errNoNetwork
}

func sysConnect(fd int, ip netip.AddrPort) error { return errNoNetwork }

func sysClose(fd int) error { return errNoNetwork }

func sysSend(fd int, buf []byte, flags int) (int, error) { return -1, errNoNetwork }

func sysRecv(fd int, buf []byte, flags int) (int, error) { return -1, errNoNetwork }

func sysSetReuseAddr(fd int) error { return errNoNetwork }

func sysSetSockOpt(fd int, level, opt int, value interface{}) error { return errNoNetwork }

func waitRead(fd int, deadline time.Time) error { return errNoNetwork }

func waitWrite(fd int, deadline time.Time) error { return errNoNetwork }

func sysLocalAddr(fd int) (netip.AddrPort, error) { return netip.AddrPort{}, errNoNetwork }

func localIPv4() (netip.Addr, error) { return netip.Addr{}, errNoNetwork }

func sysTLSConnect(fd int, hostname string) (uintptr, error) { return 0, errNoNetwork }

func sysTLSSend(state uintptr, buf []byte) (int, error) { return -1, errNoNetwork }

func sysTLSRecv(state uintptr, buf []byte) (int, error) { return -1, errNoNetwork }

func sysTLSClose(state uintptr) {}
