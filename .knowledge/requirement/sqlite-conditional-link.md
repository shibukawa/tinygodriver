---
id: requirement:sqlite-conditional-link
type: requirement
title: SQLite Amalgamation Links Only When Selected
---
Importing `database/sql/sqlite` under tinygo links the C amalgamation unconditionally, because `backend_tinygosqlite.go` is tagged `tinygo || force_tinygo_logic`; a downstream registry that imports every engine therefore drags SQLite's C into PostgreSQL-only projects, and on wasip1 that dies at `pthread.h`.

```yaml
priority: medium
requested_by: system:popcornwave, 2026-08-07, against v1.1.9
symptom: >
  tinygosqlite/amalgamation/sqlite3.c:30625: fatal error: 'pthread.h' file not
  found, in a project that uses only PostgreSQL, reached through their pw
  package importing database/sql/sqlite
root_cause_split:
  theirs: >
    the registry imports engines the project did not select; their
    rdb-dsn-resolution rule already says only the chosen engine links
  ours: >
    import-means-link C payload gives downstream no way to reference the
    package without paying for the engine
ask: >
  a build tag or sub-package split so the amalgamation links only when sqlite
  is actually chosen
shipped_2026_08_07:
  how: >
    backend_tinygosqlite.go now excludes wasip1, wasip2 and a new nosqlite tag;
    backend_none.go covers those combinations with Backend "none", still
    registering DriverName so the import compiles, and Open failing with a
    message naming both the WASI reason and the tag
  scope: >
    wasi exclusion is automatic; other tinygo targets keep linking the
    amalgamation on import unless built with -tags nosqlite. Host go is
    untouched: modernc and mattn stay as they were.
  verified: >
    wasip1 and wasip2 builds of a project importing sqlite plus pgx/stdlib
    succeed and report Backend none; force_tinygo_logic with and without
    nosqlite passes the package tests, which skip the functional cases on the
    none backend
```
