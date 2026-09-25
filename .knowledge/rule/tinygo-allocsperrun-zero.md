---
id: rule:tinygo-allocsperrun-zero
type: rule
title: AllocsPerRun Is Zero And B.Loop Is Missing Under TinyGo
---
A test built on `testing.AllocsPerRun` passes under `tinygo test` whatever the code allocates, because TinyGo's implementation returns zero. Measure `runtime.MemStats.TotalAlloc` around the loop instead; it counts on both compilers. `testing.B.Loop` is unimplemented there and panics, so a benchmark meant to run under TinyGo loops on `b.N`.

```yaml
observed: tinygo 0.42.0 linux/amd64, 2026-09-25
allocsperrun:
  symptom: >
    AllocsPerRun(10, func() { sink = make([]byte, 100) }) reports 0
  what_works: >
    ReadMemStats before and after: TotalAlloc moves by the bytes allocated
    on both compilers and is unaffected by a collection in between. Mallocs
    and HeapAlloc stay zero under TinyGo, so count bytes, not objects
  shape: >
    encoding/xmlro/reader_test.go allocatedBytes(runs, fn) uint64, with a
    test that the helper itself sees an allocation, so a future runtime
    that zeroes TotalAlloc too cannot make the suite vacuous
  consequence: >
    encoding/cbor's TestFixedShapeMessageIsZeroAllocationInSteadyState is
    such a test and has never measured anything under TinyGo. Not changed
    here; noted for whoever next touches that package
b_loop:
  symptom: "panic: unimplemented: testing.B.Loop"
  rule: >
    for range b.N in any benchmark that should run under tinygo test. The
    go1.27 crash in rule:go127-bloop-switch-ice does not arise with b.N
    either
```
