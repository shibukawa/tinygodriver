---
id: requirement:netdev-connect-validation
type: requirement
title: Connect Rejects Unusable Remote Addresses
---
`Device.Connect` must fail with a classified error when the remote address cannot be connected to, instead of handing back a socket that is not connected.

```yaml
priority: must
trigger: net.Dial("tcp4", "127.0.0.1:0")
observed_2026_07_28:
  netdev_darwin: err=nil and a conn; first write fails "socket is not connected"
  std_go: "dial tcp4 127.0.0.1:0: connect: can't assign requested address"
root_cause: rule:netdev-syscall-status
rule:
  - Connect rejects a zero remote port before calling sysConnect
  - the reject is EADDRNOTAVAIL-equivalent, message "can't assign requested address"
  - the pre-check runs on all three OSes so behavior does not depend on kernel quirks
  - the syscall path is fixed as well, so any other connect failure also surfaces
upstream_trap:
  what: tinygo net.DialTCP returns (*TCPConn)(nil) into the Conn interface on failure
  effect: conn != nil stays true after a failed Dial, so calling any method is a nil pointer dereference
  measured: "net.Dial err=socket is not connected, conn=<nil>, conn==nil is false"
  scope: tinygo net package, not netdev
  netdev_obligation: always return a non-nil error, so callers that check err never reach the trap
acceptance:
  - Dial to port 0 returns a non-nil error and no usable connection
  - the message matches std Go, so tests can assert one value on both compilers
  - the error is distinguishable from connection refused
  - tinygo net closes the fd, because Connect reported the failure
status_2026_07_28:
  state: implemented
  where: Device.Connect rejects port 0 before sysConnect, returning ErrAddrNotAvailable
  verified_tinygo: >
    net.Dial("tcp4","127.0.0.1:0") returns "can't assign requested address", and
    a second Dial in the same process returns the same error instead of a stale one
  verified_tests: netdev TestConnectPortZeroFails, TestConnectClosedPortReportsFailure
eintr_2026_08_07:
  observed: >
    intermittent "dial error: ... syscall error: errno 4" under host go with
    force_tinygo_logic; the runtime's preemption signal lands in the blocking
    connect(2) often enough to fail ordinary test runs
  fix: >
    sysConnect on darwin and linux now handles EINTR: POSIX keeps the
    handshake going in the kernel, so it waits for writability and probes
    with connect again, treating EISCONN as success and EALREADY as still in
    flight. Same family as the accept and select retry loops already there
applies_to: system:tinygo-netdev
```
