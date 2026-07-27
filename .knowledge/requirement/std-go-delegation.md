---
id: requirement:std-go-delegation
type: requirement
title: Standard Go Delegates to net/http
---
Under standard Go the package must contain no cgo and no native TLS calls; it wraps `net/http.Transport` and `crypto/tls`.

```yaml
priority: must
build_condition: "!tinygo && !force_tinygo_logic"
acceptance:
  - go build and go vet succeed with CGO_ENABLED=0
  - no import of any native backend file on this path
  - data:https-config is translated into crypto/tls.Config
  - behavior matches net/http including redirects, chunked bodies, and gzip
rationale: >
  application and test code stays shared between compilers, which is the
  established repository pattern in httpmux and compress/zstd
selection_rule: rule:build-tag-selection
