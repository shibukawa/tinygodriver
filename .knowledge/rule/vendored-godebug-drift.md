---
id: rule:vendored-godebug-drift
type: rule
title: Vendoring Changes GODEBUG Defaults
---
Vendored source runs under this module's `go` directive, not upstream's, so any behaviour gated on a GODEBUG default changes silently. Upstream tests that passed in their own module can fail here with no source difference at all.

```yaml
scope: every fork in this repository; decision:websocket-fork, decision:fasthttp-fork
mechanism: >
  GODEBUG defaults follow the go directive of the main module. Copying a file
  out of a module that says `go 1.12` into one that says `go 1.27` opts it into
  every default that changed in between
observed:
  where: gorilla/websocket prepared_test.go, decision:websocket-fork
  what: >
    the test calls rand.Seed(1234) to make the client frame mask reproducible,
    then compares two writes byte for byte. math/rand.Seed became a no-op in Go
    1.24. Upstream's go.mod says `go 1.12`, so its own run gets randseednop=0
    and the seed still works; vendored here it does nothing and all five client
    cases fail
  severity: silent behaviour change, surfaced only because a test compared bytes
fix_chosen: >
  a package variable for the mask source, set by the test. A //go:debug
  directive would fix the host build and do nothing under TinyGo, which does not
  implement godebug at all, so it is never the answer for a fork that must run
  on both compilers
rules:
  - when an upstream test fails only after vendoring, diff the go directives
    before diffing the source
  - prefer a source seam over //go:debug; the directive is invisible to TinyGo
  - record the drift in PATCHES.md, not only in the script
verify: >
  run the upstream suite unmodified in a scratch module that keeps upstream's
  go.mod, and compare. A pass there and a failure here is this defect
```
