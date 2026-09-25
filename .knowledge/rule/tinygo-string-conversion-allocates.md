---
id: rule:tinygo-string-conversion-allocates
type: rule
title: string(b) In A Comparison Allocates Under TinyGo
---
Never compare bytes to a string through `string(b) == s`, `switch string(b)` or `m[string(b)]` on a path that must not allocate under TinyGo. The Go compiler elides the conversion in those three positions; TinyGo 0.42 copies the bytes every time.

```yaml
observed: tinygo 0.42.0 linux/amd64, encoding/xmlro alloc tests, 2026-09-25
measured:
  per_comparison: 16 bytes for a 9-byte name, on each of the three forms
  host_go: 0 on all three
  unsafe_string: >
    unsafe.String(unsafe.SliceData(b), len(b)) == s allocates on neither
    compiler, and is what encoding/xmlro.Equal does behind a length check
how_it_was_missed: >
  the package's allocation test used testing.AllocsPerRun, which is a
  constant zero under TinyGo; see rule:tinygo-allocsperrun-zero. The first
  real measurement found every name comparison in the reader allocating
rule: >
  compare through a helper built on unsafe.String, and keep the helper the
  only such site so its lifetime argument is written once. In encoding/xmlro
  that is Equal, NameIs and Value.Equal
also_check: >
  encoding/cbor compares map keys and profile names; whether any of its
  zero-allocation paths uses the idiom has not been checked, and its own
  AllocsPerRun test would not say
```
