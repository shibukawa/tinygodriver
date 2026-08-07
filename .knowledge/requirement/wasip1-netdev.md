---
id: requirement:wasip1-netdev
type: requirement
title: Explicit netdev Failure on wasip1
---
A `-target=wasip1` build of anything importing netdev dies in link errors because no `sys_wasip1.go` exists; the ask is an explicit failure with a reason, not a working network stack.

```yaml
priority: high
requested_by: system:popcornwave, 2026-08-07, against v1.1.9
symptom:
  - "netdev/device.go:27: undefined: localIPv4"
  - "netdev/dns.go:162: undefined: sysClose, sysConnect, waitWrite, sysSend, waitRead, sysRecv"
root_cause: >
  sys files cover darwin, linux and windows only; GOOS=wasip1 selects none, and
  device.go and dns.go carry no build constraint, so the package half-compiles
  and the reader gets undefined symbols instead of a reason
acceptable_minimum: >
  a compile-time or startup error stating the target has no network support;
  WASI preview 1 has no outbound sockets, so a real implementation is not the
  ask. The socket-capable target is wasip2, see requirement:wasip2-no-mbedtls
  and rule:tinygo-wasip2-goos.
relation: unsupported combinations fail explicitly per requirement:platform-matrix
shipped_2026_08_07:
  how: >
    netdev/sys_wasi.go, tagged wasip1 || wasip2, stubs the whole sys surface
    with one wrapped ErrNotSupported naming the reason; register_tinygo.go
    excludes the wasi targets because TinyGo's wasi net has no useNetdev hook
  dns_change: >
    queryNameservers now wraps the last underlying failure into ErrHostUnknown
    instead of discarding it, so a wasi lookup says "no socket backend" rather
    than a bare "Host unknown". errors.Is behavior is unchanged.
  verified: >
    tinygo -target=wasip1 builds and runs under wasmtime with the explicit
    error; native darwin tinygo binary still resolves, dials and opens sqlite;
    host go netdev tests pass
```
