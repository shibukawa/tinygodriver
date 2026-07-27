---
id: system:openssl
type: system
title: OpenSSL 3 Backend
---
Not a backend of this package. Retained because system:tinygo-netdev still uses OpenSSL for its own IPPROTO_TLS path, and because the failed integration attempts justify decision:linux-mbedtls.

```yaml
state: not_used_by_this_package
status_note: >
  OpenSSL is no longer a backend anywhere. darwin excluded it to avoid Homebrew
  per decision:macos-network-framework, and linux replaced it with
  system:mbedtls per decision:linux-mbedtls. This concept is retained because
  system:tinygo-netdev still uses OpenSSL for its own IPPROTO_TLS path.
platform: linux, netdev only
model: fd_attached
availability: libssl3 runtime, libssl-dev at build time; not installed by default everywhere
symbols:
  handshake: [TLS_client_method, SSL_CTX_new, SSL_new, SSL_set_fd, SSL_connect]
  sni_and_name: [SSL_ctrl SSL_CTRL_SET_TLSEXT_HOSTNAME, SSL_set1_host]
  verify: [SSL_CTX_set_verify, SSL_CTX_set_default_verify_paths, SSL_get_verify_result]
  custom_ca: [SSL_CTX_load_verify_file, X509_STORE_add_cert, BIO_new_mem_buf, PEM_read_bio_X509]
  client_cert: [SSL_CTX_use_certificate, SSL_CTX_use_PrivateKey]
  io: [SSL_read, SSL_write, SSL_shutdown, SSL_get_error]
trust_store: default verify paths, overridable by SSL_CERT_FILE and SSL_CERT_DIR
existing_asset: >
  netdev/tls_openssl.h already implements connect, read, write, and close for
  system:tinygo-netdev and can be extended rather than rewritten
gaps_for_this_package:
  - no custom CA, client cert, or InsecureSkipVerify support
  - coarse error codes, insufficient for requirement:error-classification
  - a global mutex serializes every handshake
risk: requirement:linux-tinygo-openssl-poc
