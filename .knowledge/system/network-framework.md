---
id: system:network-framework
type: system
title: Network.framework Backend
---
macOS TLS backend that owns DNS, TCP, and TLS inside a single `nw_connection_t`.

```yaml
platform: darwin
model: connection_owning
availability: libSystem; no external dependency
setup:
  - nw_endpoint_create_host(host, port)
  - nw_parameters_create_secure_tcp with a tls options block
  - nw_connection_create, nw_connection_set_queue, nw_connection_start
  - nw_connection_set_state_changed_handler signals ready or failed
io:
  send: nw_connection_send with a completion block
  recv: nw_connection_receive with min 1 byte, delivering dispatch_data_t
  close: nw_connection_cancel, then release
verification_control:
  hook: sec_protocol_options_set_verify_block
  custom_ca: evaluate with SecTrustSetAnchorCertificates inside the verify block
  skip_verify: return true unconditionally from the verify block
  client_cert: sec_protocol_options_set_local_identity with a sec_identity_t
async_bridge:
  problem: every operation is a block callback on a dispatch queue
  approach: dispatch_semaphore_t per operation, signalled from the block
  constraint: >
    blocks must not call back into Go; the C layer stores results in a struct
    the Go side reads after the wait returns
risks: requirement:macos-blocks-poc
chosen_by: decision:macos-network-framework
