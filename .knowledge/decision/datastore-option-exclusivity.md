---
id: decision:datastore-option-exclusivity
type: decision
title: Datastore Alternatives Are Exclusive Choices
---
Datastore consistency and mutation preconditions are choices, not independent flags.

```yaml
state: accepted
accepted_on: 2026-08-10
implemented_on: 2026-08-10
read_consistency:
  choices: [strong_default, eventual, read_time, transaction, new_transaction]
  conflicts:
    - eventual with read_time
    - transaction or new_transaction with eventual or read_time
write_precondition:
  choices: [none, base_version, update_time]
  conflicts:
    - base_version with update_time
resolution:
  - retain existing ReadOption and WriteOption interfaces
  - record the selected variant and source option
  - reject a second different variant before request encoding
  - never use last-option-wins for safety or consistency semantics
acceptance:
  - every conflicting pair returns a local error without an HTTP request
  - repeated identical options are either idempotent or rejected consistently
  - valid wire output contains at most one member of each choice
implementation: option application records one variant and returns a local sentinel error on a second variant
related:
  - api:datastore-client
  - decision:datastore-write-preconditions
  - decision:configuration-resolution-boundary
```
