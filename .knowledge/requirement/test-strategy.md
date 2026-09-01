---
id: requirement:test-strategy
type: requirement
title: Test Strategy Across Compilers
---
One shared test table runs against a locally generated CA and TLS server; the host-Go build exercises the native backend of the host OS through `force_tinygo_logic`.

```yaml
priority: must
layers:
  unit_host_go:
    command: go test ./https
    server: crypto/tls.Listen with a generated ECDSA CA
    covers: the std-go delegation path
    status: passing, 15 tests
  native_backend_host_go:
    command: go test -tags force_tinygo_logic ./https
    value: native backend code is testable without a tinygo toolchain
    status: passing, 15 tests on darwin
  compile_tinygo:
    command: tinygo build ./examples/httpsclient
    value: proves requirement:no-crypto-tls-on-tinygo holds
    status: passing on darwin
  e2e_tinygo:
    runs: the built example against the same local test server
    value: the only layer that proves the tinygo runtime tolerates native calls
    status: passing on darwin; verified by spike on linux
  linux_from_darwin:
    command: >
      docker run --platform linux/arm64 -v $PWD:/src -w /src golang:1.27, then
      the two go test layers above. No package install is needed since
      decision:netdev-crypto-tls-on-linux.
    value: >
      the darwin development host has no cgo cross toolchain for linux, so a
      container is the only way to compile the //go:build linux files at all.
      That is not a convenience: netdev/sys_linux.go shipped a missing fmt
      import that no darwin build could ever have caught.
    covers: system:mbedtls for TLS, plus system:tinygo-netdev sockets and DNS
    status: >
      passing on linux/arm64 for every package, and on linux/amd64 under
      emulation for the https suite
    caveat: emulated runs prove correctness only, never throughput
  crypto_self_test:
    applies_to: system:mbedtls
    command: mbedtls_aes_self_test, gcm, sha256 and sha512 known-answer vectors
    value: >
      the only thing validating the bundled arm_neon.h in
      rule:mbedtls-hw-acceleration. Run it in both accelerated and software
      builds so a hardware path can never diverge silently.
    status: passing on linux/arm64 and linux/amd64
cases:
  - happy path GET, POST, PostForm, HEAD
  - custom CA accepted, and rejected when absent
  - hostname mismatch, expired cert, untrusted root
  - mTLS accepted on std go and linux; refused on darwin
  - InsecureSkipVerify true and false
  - timeout on a non-responding server
  - large response body, chunked encoding
  - concurrent requests
ci_matrix:
  darwin_arm64: >
    host go; host go with force_tinygo_logic; the same again with darwintls13,
    because both darwin backends ship; tinygo builds of both
  linux_arm64: same three, plus the crypto self tests
  linux_amd64:
    same: three, plus self tests
    must_be_native: >
      emulated amd64 builds of the 106 mbedTLS sources stall for hours and give
      meaningless throughput. A native runner is required.
  windows_amd64: host go only while system:schannel is provisional
