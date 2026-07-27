---
id: flow:tls-dial-tinygo
type: flow
title: TLS Dial Per Backend
---
How `dialTLS` reaches a verified connection on each backend; the three paths diverge deliberately per decision:per-os-integration-model.

```yaml
darwin:
  backend: system:network-framework
  status: shipped
  steps:
    - nw_parameters_create_secure_tcp with a tls options block from data:https-config
    - install sec_protocol_options_set_verify_block for custom CA or skip-verify
    - nw_connection_start, wait on a dispatch semaphore for the ready state
    - treat nw_connection_state_waiting as terminal, or a rejected certificate
      hangs until the caller timeout
    - nw_connection_send and nw_connection_receive, each semaphore-blocked
  ownership: C owns everything; Go holds an opaque handle
linux:
  backend: system:mbedtls
  status: shipped
  steps:
    - netdev.Device.Socket and Connect, keeping the fd; see rule:linux-socket-source
    - seed CTR-DRBG from a custom entropy source calling getrandom directly
    - mbedtls_x509_crt_parse over the anchors from rule:linux-trust-store plus
      any RootCAs in data:https-config
    - mbedtls_ssl_conf_authmode REQUIRED, or NONE for InsecureSkipVerify
    - mbedtls_ssl_conf_own_cert for client certificates, which mbedTLS takes as PEM
    - mbedtls_ssl_set_hostname, which sets SNI and the verified name together
    - mbedtls_ssl_set_bio with send and recv callbacks over the fd
    - mbedtls_ssl_handshake, retrying on WANT_READ and WANT_WRITE
  ownership: Go owns the socket; C owns the ssl context
  note: >
    the BIO callbacks are why mbedTLS net_sockets.c is dropped; tinygo's musl
    has no BSD socket API, per rule:tinygo-cgo-flag-limits
windows:
  backend: system:schannel
  status: provisional
  steps:
    - net.Dial tcp4 through system:tinygo-netdev, keep the net.Conn in Go
    - AcquireCredentialsHandle from data:https-config
    - loop InitializeSecurityContext, moving tokens over the conn
    - QueryContextAttributes for stream sizes
    - wrap conn with EncryptMessage on write and DecryptMessage on read
  ownership: Go owns everything; there is no C at all
common_postconditions:
  - peer chain and hostname verified unless InsecureSkipVerify
  - returned net.Conn honors requirement:deadline-support
  - Close releases native handles exactly once
