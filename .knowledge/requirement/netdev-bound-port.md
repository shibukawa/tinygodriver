---
id: requirement:netdev-bound-port
type: requirement
title: Bound Port Zero Resolves to the OS Assignment
---
After a successful bind with port 0, netdev must learn the port the OS assigned and report it as the socket's local address.

```yaml
priority: must
trigger: net.Listen("tcp4","127.0.0.1:0") then Listener.Addr()
observed_2026_07_28:
  netdev: 127.0.0.1:0
  std_go: 127.0.0.1:58259
cause_chain:
  - Device.Bind stores the requested AddrPort and never calls getsockname
  - the Netdever Bind signature returns only error, so the assignment cannot flow back
  - tinygo listenTCP keeps its own *TCPAddr and Addr() returns that value verbatim
scope_split:
  netdev_owns:
    - call getsockname after a successful bind and store the result in socket.laddr
    - carry the resolved laddr onto sockets produced by Accept
    - expose the resolved local AddrPort through an exported accessor
  netdev_cannot_reach:
    what: net.Listener.Addr()
    why: the listener holds a *TCPAddr that netdev is never given
    needs: an upstream tinygo net change in listenTCP
    tracked_by: decision:netdev-socket-addr-ownership
workaround: bind an explicit port, or read the port through the netdev accessor
acceptance:
  - after Bind with port 0, the accessor reports a nonzero port
  - connections from Accept report that same local port
  - behavior is identical on linux, darwin, windows
  - getsockname failure is reported, per rule:netdev-syscall-status
status_2026_07_28:
  state: implemented within the netdev boundary
  where:
    - Bind calls sysLocalAddr and stores the resolved AddrPort in socket.laddr
    - Connect stores the ephemeral local address the same way
    - Device.LocalAddr(sockfd) exposes it, as an extension beyond Netdever
  verified_tests: >
    netdev TestBindPortZeroResolves, TestAcceptCarriesResolvedLocalAddr,
    TestConnectResolvesLocalAddr
  still_open: >
    net.Listener.Addr() reports 127.0.0.1:0 under tinygo, as predicted; the
    upstream listenTCP change is not written yet
applies_to: system:tinygo-netdev
```
