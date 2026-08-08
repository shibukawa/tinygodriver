package netdev

import (
	"io"
	"net/netip"
	"time"
)

func (d *Device) GetHostByName(name string) (netip.Addr, error) {
	if name == "" {
		return netip.Addr{}, ErrHostUnknown
	}
	if ip, err := netip.ParseAddr(name); err == nil {
		if ip.Is4() {
			return ip, nil
		}
		// TinyGo net is IPv4-only for now.
		if ip.Is4In6() {
			return netip.AddrFrom4(ip.As4()), nil
		}
		return netip.Addr{}, ErrFamilyNotSupported
	}
	return lookupHost(name)
}

func (d *Device) Addr() (netip.Addr, error) {
	return localIPv4()
}

func (d *Device) Socket(domain int, stype int, protocol int) (int, error) {
	if domain != AF_INET {
		return -1, ErrFamilyNotSupported
	}

	var proto int
	var isStream bool
	switch {
	case protocol == IPPROTO_TCP && stype == SOCK_STREAM:
		proto, isStream = IPPROTO_TCP, true
	case protocol == 0 && stype == SOCK_STREAM:
		proto, isStream = IPPROTO_TCP, true
	case protocol == IPPROTO_UDP && stype == SOCK_DGRAM:
		proto, isStream = IPPROTO_UDP, false
	case protocol == 0 && stype == SOCK_DGRAM:
		proto, isStream = IPPROTO_UDP, false
	case protocol == IPPROTO_TLS && stype == SOCK_STREAM:
		proto, isStream = IPPROTO_TLS, true
	default:
		return -1, ErrProtocolNotSupported
	}

	socketProto := proto
	if proto == IPPROTO_TLS {
		socketProto = IPPROTO_TCP
	}
	fd, err := sysSocket(AF_INET, stype, socketProto)
	if err != nil {
		return -1, err
	}

	d.mu.Lock()
	d.sockets[fd] = &socket{fd: fd, protocol: proto, isStream: isStream}
	d.mu.Unlock()
	return fd, nil
}

func (d *Device) Bind(sockfd int, ip netip.AddrPort) error {
	s, err := d.get(sockfd)
	if err != nil {
		return err
	}
	// SO_REUSEADDR must be set before bind.
	_ = sysSetReuseAddr(s.fd)
	if err := sysBind(s.fd, ip); err != nil {
		return err
	}
	// Port 0 asks the OS to pick; the choice is only readable through
	// getsockname, so record the resolved address rather than the request.
	s.mu.Lock()
	s.laddr = ip
	if actual, err := sysLocalAddr(s.fd); err == nil {
		s.laddr = actual
	}
	s.mu.Unlock()
	return nil
}

// LocalAddr returns the address the OS assigned to sockfd, including the port
// chosen for a bind on port 0.
//
// This is an extension beyond the Netdever interface. TinyGo's net.Listener
// keeps its own copy of the requested address and has no way to receive this
// value, so net.Listener.Addr() still reports port 0; see
// requirement:netdev-bound-port.
func (d *Device) LocalAddr(sockfd int) (netip.AddrPort, error) {
	s, err := d.get(sockfd)
	if err != nil {
		return netip.AddrPort{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.laddr, nil
}

func (d *Device) Connect(sockfd int, host string, ip netip.AddrPort) error {
	s, err := d.get(sockfd)
	if err != nil {
		return err
	}
	addr := ip
	if !addr.Addr().IsValid() && host != "" {
		resolved, err := d.GetHostByName(host)
		if err != nil {
			return err
		}
		addr = netip.AddrPortFrom(resolved, ip.Port())
	}
	// Port 0 means "let the OS choose" for bind, but it is not a destination.
	// Reject it here so every OS behaves like standard Go instead of leaving
	// the caller with a socket that was never connected.
	if addr.Port() == 0 {
		return ErrAddrNotAvailable
	}
	if err := sysConnect(s.fd, addr); err != nil {
		return err
	}
	if s.protocol == IPPROTO_TLS {
		tls, err := sysTLSConnect(s.fd, host)
		if err != nil {
			return err
		}
		s.tls = tls
	}
	s.mu.Lock()
	s.raddr = addr
	if actual, err := sysLocalAddr(s.fd); err == nil {
		s.laddr = actual
	}
	s.mu.Unlock()
	return nil
}

// minListenBacklog is the accept-queue depth Listen will not go below.
// It matches the SOMAXCONN that host kernels default to.
const minListenBacklog = 128

func (d *Device) Listen(sockfd int, backlog int) error {
	s, err := d.get(sockfd)
	if err != nil {
		return err
	}
	// TinyGo's net hardcodes a backlog of 5, which is sized for a
	// microcontroller serving one client at a time. On a host, a burst of
	// concurrent connects overflows that queue and the kernel resets the
	// overflow, so raise it to what a host listener would ask for.
	if backlog < minListenBacklog {
		backlog = minListenBacklog
	}
	_ = sysSetReuseAddr(s.fd)
	return sysListen(s.fd, backlog)
}

func (d *Device) Accept(sockfd int) (int, netip.AddrPort, error) {
	s, err := d.get(sockfd)
	if err != nil {
		return -1, netip.AddrPort{}, err
	}
	if !s.isStream {
		return -1, netip.AddrPort{}, ErrProtocolNotSupported
	}

	// Blocking accept (http.Server manages concurrency via goroutines).
	nfd, raddr, err := sysAccept(s.fd)
	if err != nil {
		// Close() during a blocked accept surfaces as a raw errno, which a
		// server cannot tell apart from a real failure. If the listener is gone
		// from the table, the close is what ended the accept.
		if _, gone := d.get(sockfd); gone != nil {
			return -1, netip.AddrPort{}, ErrClosed
		}
		return -1, netip.AddrPort{}, err
	}

	// The listener's laddr is the resolved one, so a server bound to port 0
	// still reports where it is on every accepted connection.
	s.mu.Lock()
	laddr := s.laddr
	s.mu.Unlock()

	d.mu.Lock()
	d.sockets[nfd] = &socket{
		fd:       nfd,
		protocol: IPPROTO_TCP,
		isStream: true,
		laddr:    laddr,
		raddr:    raddr,
	}
	d.mu.Unlock()
	return nfd, raddr, nil
}

func (d *Device) Send(sockfd int, buf []byte, flags int, deadline time.Time) (int, error) {
	s, err := d.get(sockfd)
	if err != nil {
		return -1, err
	}
	if len(buf) == 0 {
		return 0, nil
	}
	if err := waitWrite(s.fd, deadline); err != nil {
		return -1, err
	}
	if s.protocol == IPPROTO_TLS {
		// Writers serialize on writeMu only, so a Recv blocked in the TLS
		// stack cannot stall this Send; see the lock comment on socket.
		s.writeMu.Lock()
		defer s.writeMu.Unlock()
		s.mu.Lock()
		tls := s.tls
		s.mu.Unlock()
		return sysTLSSend(tls, buf)
	}
	n, err := sysSend(s.fd, buf, flags)
	if n < 0 {
		return -1, err
	}
	return n, err
}

func (d *Device) Recv(sockfd int, buf []byte, flags int, deadline time.Time) (int, error) {
	s, err := d.get(sockfd)
	if err != nil {
		return -1, err
	}
	if len(buf) == 0 {
		return 0, nil
	}
	if err := waitRead(s.fd, deadline); err != nil {
		return -1, err
	}
	if s.protocol == IPPROTO_TLS {
		s.readMu.Lock()
		defer s.readMu.Unlock()
		s.mu.Lock()
		tls := s.tls
		s.mu.Unlock()
		n, err := sysTLSRecv(tls, buf)
		if n == 0 && err == nil {
			return 0, io.EOF
		}
		return n, err
	}
	n, err := sysRecv(s.fd, buf, flags)
	if n == 0 && err == nil && s.isStream {
		return 0, io.EOF
	}
	if n < 0 {
		return -1, err
	}
	return n, err
}

func (d *Device) Close(sockfd int) error {
	d.mu.Lock()
	s, ok := d.sockets[sockfd]
	if ok {
		delete(d.sockets, sockfd)
	}
	d.mu.Unlock()
	if !ok {
		return ErrInvalidSocketFd
	}
	// Both I/O locks, in the readMu → writeMu order Send and Recv respect, so
	// the TLS session is never freed under an operation still using it.
	s.readMu.Lock()
	s.writeMu.Lock()
	s.mu.Lock()
	if s.tls != 0 {
		sysTLSClose(s.tls)
		s.tls = 0
	}
	s.mu.Unlock()
	s.writeMu.Unlock()
	s.readMu.Unlock()
	if err := sysClose(s.fd); err != nil {
		return ErrClosingSocket
	}
	return nil
}

func (d *Device) SetSockOpt(sockfd int, level int, opt int, value interface{}) error {
	s, err := d.get(sockfd)
	if err != nil {
		return err
	}
	return sysSetSockOpt(s.fd, level, opt, value)
}

func (d *Device) get(fd int) (*socket, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	s, ok := d.sockets[fd]
	if !ok {
		return nil, ErrInvalidSocketFd
	}
	return s, nil
}
