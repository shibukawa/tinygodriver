---
id: rule:go127-bloop-switch-ice
type: rule
title: No Tagless Switch Inside b.Loop On go1.27.0
---
Do not write a tagless `switch { case f(): ... }` inside a `for b.Loop() { ... }` body. go1.27.0's escape analysis crashes on it with an internal compiler error blamed on `iter/iter.go:223`, the declaration of `iter.Seq`, which names nothing in the file being compiled.

```yaml
observed: go1.27.0 linux/amd64, encoding/xml/bench_test.go BenchmarkSstDecode_Reader, 2026-09-24
symptom: >
  "internal compiler error: panic: runtime error: invalid memory address or
  nil pointer dereference" at iter.go:223:6, from
  escape.(*escape).stmt through discards on a switch case list whose
  expression has no type. -gcflags=-l does not avoid it
how_to_recognise: >
  the position is in the standard library's iter package and the package
  being compiled does not import it. Bisect by benchmark function, not by
  line: the crash is in the b.Loop rewrite of the enclosing function
workaround: >
  an if-else chain in place of the switch, or a classic for i := 0; i < b.N
  loop. The same switch outside a b.Loop body compiles
caveat: >
  do not trust a bisection that checks the exit code alone. Removing code can
  leave an import unused, and that compile error looks like the same failure
  from a script
```
