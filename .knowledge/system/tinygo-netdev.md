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
  - IPPROTO_TLS, a darwin-only OpenSSL path used by TinyGo net.Dial
relationship:
  linux: dialTLS uses a netdev TCP socket and drives system:mbedtls over its fd
  windows: dialTLS uses a netdev TCP socket; system:schannel only transforms buffers
  darwin: bypassed entirely, because system:network-framework does its own DNS and TCP
  std_go: not involved; netdev registration is a no-op
constraints_inherited:
  - IPv4 only
  - blocking accept and connect
  - no unix domain sockets, per rule:netdev-no-unix-socket
defects_fixed_2026_07_28:
  syscall_status: rule:netdev-syscall-status
  dial_to_port_zero: requirement:netdev-connect-validation
  bound_port_zero: requirement:netdev-bound-port
still_open:
  listener_addr: >
    net.Listener.Addr() reports port 0 for a port 0 listen; the remaining fix is
    upstream in tinygo net, see decision:netdev-socket-addr-ownership
  windows_untested: the winsock error mapping was written without a windows host
overlap:
  concern: IPPROTO_TLS and api:tls-dialer both provide TLS on darwin
  resolution: >
    keep IPPROTO_TLS for net.Dial("tls") users; api:https-transport does not use
    it, because per-request data:https-config cannot be expressed through a
    socket protocol number
