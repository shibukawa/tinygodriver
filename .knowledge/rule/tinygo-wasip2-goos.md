---
id: rule:tinygo-wasip2-goos
type: rule
title: TinyGo wasip2 Builds as GOOS linux
---
TinyGo's `-target=wasip2` compiles with GOOS=linux, so every `linux` build constraint in this repository is admitted into a wasip2 build unless it also excludes the `wasip2` tag.

```yaml
measured: tinygo 0.41.1, `tinygo info -target=wasip2`
wasip2: {goos: linux, goarch: arm, extra_tags: [tinygo.wasm, wasip2]}
wasip1: {goos: wasip1, goarch: wasm, extra_tags: [tinygo.wasm]}
consequence:
  - >
    internal/mbedtls admits its cgo files on wasip2, then dies on the missing C
    sysroot; see requirement:wasip2-no-mbedtls
  - >
    netdev/sys_linux.go is admitted the same way, so netdev appears to have a
    wasip2 backend that cannot work
  - >
    wasip1 is the opposite failure: GOOS=wasip1 selects no sys file at all, so
    netdev links against undefined symbols; see requirement:wasip1-netdev
exclusion_pattern: >
  add !wasip2 next to linux, or gate on !tinygo.wasm to cover both wasm targets
  in one constraint
```
