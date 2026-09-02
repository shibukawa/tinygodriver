// Package syncx holds the one synchronization primitive this repository
// cannot take from the standard library on every compiler.
//
// TinyGo's sync.RWMutex (through at least 0.42) deadlocks whenever a reader
// arrives while a writer is waiting for the existing readers to drain: the
// new reader adds itself to the reader count before it blocks, so the count
// never returns to the value the writer is waiting for, and the reader in turn
// waits for the writer's Unlock. Upstream fix: tinygo-org/tinygo#5630.
//
// RWMutex here is sync.RWMutex on standard Go and a plain sync.Mutex behind
// the same method set on the TinyGo path. Every critical section in this
// repository that used to sit under an RWMutex is a map lookup, so giving up
// reader parallelism costs nothing measurable, and a Mutex cannot hit the
// defect. Retire the shim when a TinyGo release ships the upstream fix;
// TestUpstreamRWMutexStillDeadlocks fails the day that happens.
package syncx
