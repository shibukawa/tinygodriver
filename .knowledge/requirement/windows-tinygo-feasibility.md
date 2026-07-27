---
id: requirement:windows-tinygo-feasibility
type: requirement
title: Windows Backend, Provisional
---
The Windows design is recorded but deliberately not finalized; nothing here has been verified on a Windows machine.

```yaml
priority: should
state: provisional
scope_note: >
  held provisional on purpose. Everything below is design intent from reading
  the APIs, not measurement, and should be re-derived when Windows work starts.
settled_by_reasoning:
  language: pure go via syscall, no cgo
  reason: >
    SSPI is a flat C ABI with no callbacks, so syscall covers it. This also
    avoids a mingw toolchain and rule:cgo-bridge-contract entirely.
  model: buffer transform; Go owns the socket
open_questions:
  tinygo_syscall_dll:
    check: does tinygo windows/amd64 support syscall.NewLazyDLL and LazyProc.Call
    fallback_if_no: windows stays std-go only until tinygo gains support
  tinygo_windows_maturity:
    check: does tinygo 0.41 windows/amd64 build and run the existing examples
    note: system:tinygo-netdev already has sys_windows.go, so sockets are assumed working
  sspi_loop:
    check: >
      AcquireCredentialsHandle, the InitializeSecurityContext retry loop,
      QueryContextAttributes for stream sizes, EncryptMessage, DecryptMessage
    note: DecryptMessage returns SEC_E_INCOMPLETE_MESSAGE and SEC_I_RENEGOTIATE cases
  client_certificates:
    check: whether PEM can be imported without touching the user's cert store
sequencing: >
  the pure-Go implementation is testable on host go windows independently of
  tinygo, so implementation need not wait for the tinygo question
until_then:
  behavior: api:tls-dialer returns ErrPlatformNotSupported on tinygo windows
  std_go: unaffected; requirement:std-go-delegation already covers windows
