---
id: system:go-sql-driver-mysql
type: system
title: go-sql-driver/mysql Driver
---
Maintained third-party MySQL and MariaDB driver used as the base of both paths of the `database/sql/mysql` package: unmodified on host go, forked as `tinygomysql` on tinygo.

```yaml
import: github.com/go-sql-driver/mysql
version_validated: v1.10.0
fork: database/sql/mysql/tinygomysql
provides:
  - text and binary protocol, prepared statements, transactions
  - caching_sha2_password with RSA-OAEP, MariaDB ed25519, all verified under tinygo
  - multiStatements, sending several statements in one comQuery
  - interpolateParams, client-side parameter inlining that skips prepare
  - LOAD DATA LOCAL INFILE through RegisterReaderHandler
absent:
  pipelining: >
    no api sends several commands before reading the first reply; the connection
    is strictly one command at a time
  bulk_execute: >
    MariaDB COM_STMT_BULK_EXECUTE is not implemented, and MySQL has no equivalent
    command at all
  consequence: >
    multiStatements is the only round-trip amortization the driver exposes; see
    decision:batch-transport-per-driver
extension_points_used:
  mysql.Result: >
    public interface over driver.Result adding AllRowsAffected() []int64 and
    AllLastInsertIds() []int64, one entry per executed statement. Documented for
    sql.Conn.Raw, and the same shape exists in the fork, so a structural
    interface reaches both without importing either
  driver.RowsNextResultSet: mysqlRows implements it, so multi-statement result sets are walkable
  sql.Conn.Raw: the only way to reach mysql.Result, since sql.Result hides it
tinygo_gap:
  compile: crypto/tls stub lacks tls.Config.Clone, breaking dsn.go and utils.go
  runtime:
    conn_check: connCheck needs syscall.Conn, which tinygo errors on, so pooling dies
    tls: tls.Client panics; the STARTTLS-style upgrade needs api:tls-upgrade
config_resolution: decision:mysql-config-resolution
```
