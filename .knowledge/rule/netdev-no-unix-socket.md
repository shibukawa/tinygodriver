---
id: rule:netdev-no-unix-socket
type: rule
title: Unix Domain Sockets Are Out of Scope
---
netdev does not support `unix`, `unixgram`, or `unixpacket` networks, and will not add them. The limit is structural, not an unfinished feature.

```yaml
measured_2026_07_28: tinygo 0.41.1
reasons:
  upstream: >
    tinygo net.Dial and net.Listen reject these networks before any netdev call;
    tinygo unixsock.go ships address types only, with no dial or listen path
  interface: >
    Netdever addresses every socket as netip.AddrPort, which cannot carry a
    filesystem path, so no bind or connect argument could express the endpoint
  domain: Device.Socket accepts AF_INET only and returns ErrFamilyNotSupported otherwise
observed:
  tinygo: net.Dial("unix", path) -> "Network unix not supported"
  std_go: reaches the OS and reports "connect: no such file or directory"
consequence: >
  adding support would mean forking tinygo net and widening the Netdever
  interface, which is out of proportion to the need
guidance: use tcp4 on 127.0.0.1 for local IPC under tinygo
status_2026_07_28:
  state: documented, no code change needed
  where: netdev README notes, and Socket already returns ErrFamilyNotSupported
  verified_tests: netdev TestUnixDomainUnsupported
applies_to: system:tinygo-netdev
```
