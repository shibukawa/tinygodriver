---
id: rule:linux-socket-source
type: rule
title: Linux Socket Comes From netdev, Not net.Dial
---
The Linux backend creates its socket through `netdev.Device` directly, because TinyGo gives no way to recover a file descriptor from a `net.Conn`.

```yaml
measured_2026_07_27: tinygo 0.41.1, linux/arm64
what_works:
  net_dial: >
    net.Dial("tcp4", "host:port") succeeds under tinygo once
    system:tinygo-netdev is registered, and it resolves hostnames
what_does_not:
  syscall_conn:
    detail: TCPConn.SyscallConn returns "SyscallConn not implemented"
    consequence: the fd behind a net.Conn is unreachable
  net_lookup_ip:
    detail: net.LookupIP does not exist in tinygo's net package
    consequence: Go cannot resolve a name separately from dialing
solution:
  use: netdev.Device.Socket and netdev.Device.Connect
  why_it_is_fine: >
    netdev is this repository's own package and its methods are exported.
    Connect resolves the host itself when the address is invalid, so DNS still
    works, and the descriptor is an ordinary OS fd that mbedTLS can drive.
  ownership: Go owns the fd and closes it; the C layer only reads and writes it
consequence_to_document:
  detail: >
    the linux native path imports netdev, and netdev uses OpenSSL on host-Go
    builds, so `go test -tags force_tinygo_logic` on linux needs libssl-dev.
    TinyGo builds do not, because netdev's tinygo linux TLS is a stub.
  follow_up: >
    making netdev's OpenSSL path optional behind a build tag would remove this
    from the test environment
applies_to: flow:tls-dial-tinygo
