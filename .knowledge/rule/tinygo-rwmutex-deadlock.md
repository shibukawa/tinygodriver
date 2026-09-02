---
id: rule:tinygo-rwmutex-deadlock
type: rule
title: No sync.RWMutex on the TinyGo Path
---
TinyGo's `sync.RWMutex` (through at least 0.42) deadlocks whenever a reader arrives while a writer is waiting for the existing readers to drain, so every lock a TinyGo build can reach uses `internal/syncx.RWMutex`, which is the standard type on standard Go and a plain mutex on TinyGo.

```yaml
scope: every non-test file a `-tags tinygo` build compiles, on darwin, linux and windows
measured: 2026-09-02, tinygo 0.42.0 darwin/arm64, scheduler threads
mechanism: >
  src/sync/mutex.go. Lock() subtracts rwMutexMaxReaders from the reader count
  and waits for the last RUnlock to bring it back to exactly
  -rwMutexMaxReaders. RLock() does readers.Add(1) and then waits for the count
  to turn positive, keeping its +1 while it waits. So a reader that arrives
  mid-wait is counted as a holder that never leaves: the last real RUnlock
  lands on -rwMutexMaxReaders+1, the writer is never woken, and the new reader
  waits for the writer's Unlock. Standard Go avoids this with the separate
  readerWait counter the writer snapshots
four_step_reproducer: >
  R1 RLock; W Lock in a goroutine; sleep; R2 RLock in a goroutine; sleep; R1
  RUnlock. Standard Go finishes, tinygo 0.42 never does. Committed as
  internal/syncx fourStep, run against sync.RWMutex by
  TestUpstreamRWMutexStillDeadlocks (tinygo only) and against the shim by
  TestRWMutexHandsOverToWaitingWriter (every build)
evidence:
  - >
    stack sample of a hung `tinygo test ./websocket` binary: 15 threads in
    RWMutex.RLock, 1 in RWMutex.Lock, none in any socket syscall
  - pure RWMutex stress, 16 readers + 1 writer around a map, no netdev: 13 of 13 hung
  - plain netdev TCP echo, 16 goroutines x 50 round trips, no websocket: 11 of 40 hung
  - the same probe with netdev.Device.mu as a plain sync.Mutex: 0 of 40 hung
  - websocket TestConcurrentClients, the original symptom: 5 of 16 hung, see rule:tinygo-test-constraints
upstream: >
  tinygo-org/tinygo#5630 "sync: hand the RWMutex over instead of a re-test of
  the reader count", open as of 2026-09-02. Not in 0.42
what_was_at_risk:
  netdev.Device.mu: get() on every Send/Recv against Socket/Accept/Close; the hang
  netdev sessionMu (darwin, windows): every TLS Send/Recv against each connect and close
  fasthttp HostClient.mLock: every request against the first request to a new host
  nosql/dynamodb and nosql/datastore fieldCache: every marshal against the first sight of a type
  storage/s3 mu: every request against a redirect
  tinygomysql serverPubKeyLock: authentication against the key cache
  registration_only: httpmux, http.ServeMux, tinygomysql dial and TLS registries; safe in practice, swapped anyway
rules:
  - declare read-mostly locks as internal/syncx.RWMutex, never sync.RWMutex, in any file a tinygo build compiles
  - >
    the shim serializes readers. Every site here guards a map lookup, so that
    costs nothing measurable; a hot read path that genuinely needs reader
    parallelism on TinyGo needs a design discussion, not an exception
  - a hung tinygo binary needs SIGKILL; Go ignores SIGALRM, so a perl/alarm deadline does nothing
enforced_by: >
  internal/syncx TestNoStdRWMutexOnTinyGoPath asks `go list -tags tinygo` for
  the files of every package on each GOOS and parses them for the selector,
  so comments may say sync.RWMutex and vendored forks cannot drift back
retire_when: >
  a TinyGo release ships the upstream fix. TestUpstreamRWMutexStillDeadlocks
  fails on that release; then delete rwmutex_tinygo.go, make the alias
  unconditional, and drop the policy test
vendored_forks: >
  fasthttp carries the swap as vendor.py patches (PATCHES.md section 8);
  tinygomysql is hand-vendored and the swap is recorded in its README
```
