---
id: decision:mysql-config-resolution
type: decision
title: MySQL Keeps Raw And Resolved Config Separate
---
The TinyGo MySQL fork must not store named configuration and its resolved object as two writable truths.

```yaml
state: accepted
accepted_on: 2026-08-10
implemented_on: 2026-08-10
current_pairs:
  tls: TLSConfig name, TLS pointer, and AllowFallbackToPlaintext derived from preferred
  server_key: ServerPubKey name and private pubKey pointer
target:
  public_config: retains source-compatible raw fields and explicit TLS override
  resolved_config: internal clone containing final TLS, fallback policy, server key, address, and defaults
precedence:
  tls_pointer: explicit override of TLSConfig, preserved for compatibility
  tls_name: resolved only when no explicit pointer exists
  preferred: resolves to TLS plus fallback in resolved_config only
  server_key_name: always re-resolved; empty clears the resolved key
rules:
  - normalize returns resolved_config and does not mutate Config
  - NewConnector resolves once and connections clone resolved_config
  - FormatDSN serializes only representable raw fields
  - changing a raw name before NewConnector cannot retain an older object
tests:
  - parse, change TLSConfig, then connect uses the new name
  - parse, clear ServerPubKey, then no old key remains
  - FormatDSN followed by ParseDSN preserves every serializable choice
implementation: Config.resolve clones first, tracks derived provenance internally, and NewConnector retains only the resolved clone
related:
  - system:go-sql-driver-mysql
  - data:https-config
  - decision:configuration-resolution-boundary
```
