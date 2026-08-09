---
id: rule:tinygo-reflect-func-pointer
type: rule
title: TinyGo reflect.Value.Pointer on funcs
---
Never compare function identity through `reflect.ValueOf(f).Pointer()` under TinyGo. A func value there is a `{context, code}` pair; `Pointer()` does not reliably return the code address, and the context word of a non-capturing closure is unstable — the same handler observed with a static address, `0x1`, and `nil`, varying with how the value was stored and reloaded. The function still calls correctly; only reflection misreports it.

```yaml
observed: TinyGo 0.41.1 darwin/arm64, fasthttprouter TestRouterMutable, 2026-08-09
symptom: >
  handler stored into a struct field and read back calls the right function
  but reflects to a pointer unrelated to the original; simple cases pass,
  which makes the failure look data-dependent
workaround: >
  compare the code word via unsafe: cast &f to a {context, code unsafe.Pointer}
  pair and compare .code. Build-tag the reflect version for host go. See
  fasthttprouter/handleridentity_tinygo_test.go.
caveat: code-word comparison treats closures over one literal with different captures as equal
```
