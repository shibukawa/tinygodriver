---
id: rule:netdev-syscall-status
type: rule
title: Syscall Failure Comes From the Result, Not errno
---
Every netdev syscall wrapper must decide success or failure from the syscall return value itself, and must carry the numeric code with the failure. Reading a separate `errno` after a raw syscall is unsound, because a raw syscall never writes libc `errno`.

```yaml
measured_2026_07_28: darwin/arm64, tinygo 0.41.1, go 1.26.5
per_platform:
  linux:
    mechanism: raw_syscall6 returns -errno in the result register
    handling: syscall_result maps [-4095,-1] to -1 and stores netdev_errno
    status: correct
  darwin:
    mechanism: svc #0x80 returns errno in x0 and sets the carry flag on failure
    fixed_2026_07_28: >
      svc3 and svc6 capture the flag with cset, store the code in netdev_errno,
      and return -1; raw-syscall call sites read it through lastSysErrno, while
      read write close select keep using libc errno through lastErrno
    was: svc3 and svc6 discarded the carry flag and never set libc errno
    chain:
      - h_connect returns a positive errno instead of -1
      - sysConnect sees nonzero and calls lastErrno
      - lastErrno reads *__error(), which the raw svc never wrote
      - sysErrno(0) returns nil, so a failed call is reported as success
      - a stale errno left by an earlier libc call surfaces as an unrelated error
    affected: h_socket h_bind h_listen h_accept h_connect h_setsockopt h_getsockname h_recvfrom h_sendto
    unaffected: read write close fcntl select, which go through libc and do set errno
    fix: capture the carry flag in the asm block (cset) and return -1 plus the code
  windows:
    mechanism: INVALID_SOCKET or SOCKET_ERROR, with WSAGetLastError for the code
    fixed_2026_07_28: lastErrno maps WSAGetLastError onto the shared classes
    was: lastErrno returned the constant "winsock error"
    unverified: no windows host was available; compile and behavior are untested
required:
  - never return a nil error when the underlying call failed
  - never derive a failure code from state the failing call did not write
  - a failure with no recorded code degrades to ErrSyscall, never to nil
  - map the numeric code onto the shared error classes in netdev.go
eintr:
  why: >
    correct detection makes EINTR visible for the first time, and accept blocks
    for the life of a server, so a signal would otherwise end the accept loop
  handling: >
    sysAccept retries on EINTR on linux and darwin; waitFD already did; since
    2026-09-02 sysSend and sysRecv retry too, inside Device.Send's full-write
    loop, see rule:netdev-write-fully
error_classes: >
  ErrAddrNotAvailable ErrAddrInUse ErrConnRefused ErrConnReset ErrNotConnected
  ErrConnTimedOut ErrWouldBlock ErrSyscall, so errors.Is behaves the same on all
  three OSes and the messages match standard Go wording
evidence_darwin: >
  net.Dial("tcp4","127.0.0.1:0") returned err=nil plus a socket whose first write
  failed with ENOTCONN, and a later Dial in the same process reported that stale
  ENOTCONN as its own connect error
causes: requirement:netdev-connect-validation
applies_to: system:tinygo-netdev
```
