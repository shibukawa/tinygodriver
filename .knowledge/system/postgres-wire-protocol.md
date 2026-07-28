---
id: system:postgres-wire-protocol
type: system
title: PostgreSQL Frontend/Backend Protocol v3
---
The message protocol both paths of decision:postgres-backend-split speak; implemented by the pgproto3 package of system:pgx, not by this repository. Recorded as background for reading and patching that code.

```yaml
version: 3.0
transport: single tcp stream, length-prefixed typed messages
startup:
  - StartupMessage carries user, database, and parameters
  - no SSLRequest is sent on the tinygo path, per decision:postgres-tls-via-proxy
auth_validated_under_tinygo:
  - AuthenticationSASL with scram-sha-256, over crypto/hmac, sha256, pbkdf2
  - AuthenticationMD5Password, over crypto/md5
  - AuthenticationOk
query_paths:
  simple: Query, RowDescription, DataRow, CommandComplete, ReadyForQuery
  extended: Parse, Bind, Describe, Execute, Sync
cancellation:
  message: CancelRequest on a second connection
  needs: backend pid and secret key from BackendKeyData at startup
  mandated_by: rule:postgres-query-cancellation
  implemented_by: pgconn.CancelRequestContextWatcherHandler, already upstream
transport_constraints:
  from: system:tinygo-netdev
  ipv4_only: yes
  unix_socket: not available, so the local default socket path cannot be used
