---
id: requirement:macos-blocks-poc
type: requirement
title: Spike — Network.framework Under TinyGo cgo
---
Resolved: a TinyGo binary completed a TLS handshake and HTTP round trip through Network.framework, so decision:macos-network-framework is unblocked.

```yaml
priority: must
state: resolved
resolved_on: 2026-07-26
toolchain: tinygo 0.41.1, go 1.26.5, darwin/arm64, MacOSX.sdk 27.0
verdict: proceed with system:network-framework; the fallback is not needed
gates_passed:
  blocks: Clang blocks compile and link, including nested blocks and __block captures
  dispatch_bridge: dispatch_semaphore_wait blocks the caller and libdispatch threads still signal it
  handshake: TLS handshake plus HTTP GET against a local TLS server
  verify_block: sec_protocol_options_set_verify_block overrides verification
  rejection: default verification rejects an untrusted cert with OSStatus -9808
  concurrency: 4 concurrent goroutines each completed their own dial
findings_that_changed_the_design:
  waiting_state_is_terminal:
    symptom: certificate rejection hung until the caller timeout rather than failing
    cause: nw_connection parks recoverable errors in state waiting and retries forever
    fix: treat nw_connection_state_waiting as terminal for a client dial
    effect: rejection latency dropped from 10.0s to 0.22s
  osstatus_is_available:
    detail: nw_error_get_error_code returns real Secure Transport OSStatus values
    effect: satisfies requirement:error-classification without extra plumbing
  no_callbacks_into_go:
    detail: blocks run on libdispatch threads and only touch C memory, then signal
    effect: confirms the rule in rule:cgo-bridge-contract is workable
constraints_discovered: rule:tinygo-darwin-toolchain
