---
id: system:tinygo-netdev
type: system
title: netdev Host Driver
---
Existing in-repo Netdever that gives TinyGo `net` and `net/http` a host TCP/IP stack; the new package builds above it, not inside it.

```yaml
import: github.com/shibukawa/tinygodriver/netdev
provides:
  - Socket, Connect, Send, Recv, Close over BSD sockets on linux, darwin, windows
  - GetHostByName via /etc/hosts plus a UDP A-record resolver
  - IPPROTO_TLS, a darwin-only Secure Transport path used by TinyGo net.Dial
relationship:
  linux: dialTLS uses a netdev TCP socket and drives system:mbedtls over its fd
  windows: dialTLS uses a netdev TCP socket; system:schannel only transforms buffers
  darwin: >
    dialTLS uses system:network-framework, which owns its own DNS and TCP;
    upgradeTLS adopts a netdev fd through Secure Transport. See
    decision:darwin-hybrid-tls
  std_go: not involved; netdev registration is a no-op
constraints_inherited:
  - IPv4 only
  - blocking accept and connect
  - no unix domain sockets, per rule:netdev-no-unix-socket
  - no IPv6; a [::1] literal fails at name lookup
defects_fixed_2026_07_28:
  syscall_status: rule:netdev-syscall-status
  dial_to_port_zero: requirement:netdev-connect-validation
  bound_port_zero: requirement:netdev-bound-port
  tls_attach_point:
    was: sysTLSConnect ran only inside Device.Connect, so a live socket could not upgrade
    now: requirement:tls-upgrade-seam, satisfied by upgradeTLS(fd, ...) on darwin and linux
still_open:
  listener_addr: >
    net.Listener.Addr() reports port 0 for a port 0 listen; the remaining fix is
    upstream in tinygo net, see decision:netdev-socket-addr-ownership
  windows_untested: the winsock error mapping was written without a windows host
  local_addr: >
    net.Conn.LocalAddr() still returns nil even though Device.LocalAddr(sockfd)
    now exists; tinygo net does not call it. Same root as listener_addr
  deadline_not_interruptible: >
    waitFD snapshots the deadline on entry and blocks in select(), so a
    SetDeadline from another goroutine cannot abort a read already in flight.
    Drives rule:postgres-query-cancellation
  missing_std_api: net.Resolver, net.DefaultResolver, net.LookupIP, TCPConn.SetNoDelay
  scheduler: blocking cgo calls require -scheduler=threads, see rule:tinygo-threads-scheduler
  measured_by: requirement:postgres-driver-validation, re-verified after the 2026-07-28 rebase
overlap:
  concern: IPPROTO_TLS and api:tls-dialer both provide TLS on darwin
  resolution: >
    keep IPPROTO_TLS for net.Dial("tls") users; api:https-transport does not use
    it, because per-request data:https-config cannot be expressed through a
    socket protocol number
