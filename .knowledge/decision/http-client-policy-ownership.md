---
id: decision:http-client-policy-ownership
type: decision
title: Separate Operation Budgets From HTTP Transport Ownership
---
Service clients own logical-operation policy; a supplied `http.Client` owns transport and pool policy.

```yaml
state: accepted
accepted_on: 2026-08-10
implemented_on: 2026-08-10
operation_policy:
  timeout: >
    WithTimeout is one budget for the whole public operation, including retries,
    backoff, redirects, response body, token refresh, and checksum retry
  mechanism: derive one context deadline before the first attempt
  applies_with_custom_http_client: true
transport_policy:
  owned_client: WithMaxIdleConns configures the internally created transport
  supplied_client: its Transport, pool, per-request timeout, and Close lifecycle belong to the caller
  conflict: WithHTTPClient plus WithMaxIdleConns returns a constructor error
  close: close idle connections only for an internally created client
precedence:
  request_context: earlier caller deadline wins
  supplied_http_timeout: may shorten an attempt but never extends operation timeout
targets:
  - api:s3-client
  - api:dynamodb-client
  - api:datastore-client
retry_contract:
  - requirement:dynamodb-retry-policy
  - requirement:datastore-retry-policy
compatibility: WithTimeout becomes stronger and matches its documented logical meaning
implementation: S3, DynamoDB, and Datastore derive one operation context before any retry or redirect
```
