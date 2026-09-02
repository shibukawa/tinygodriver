//go:build tinygo || force_tinygo_logic

package syncx

import "sync"

// RWMutex is a plain Mutex wearing the RWMutex method set. Readers serialize
// behind one another, which is the price of not deadlocking: TinyGo's own
// RWMutex loses a writer's wakeup as soon as a reader arrives mid-wait. See
// the package comment and TestUpstreamRWMutexStillDeadlocks.
//
// force_tinygo_logic takes this build too, so the shim runs under `go test`
// and the host suites exercise the same locking the TinyGo binary ships.
type RWMutex struct {
	mu sync.Mutex
}

// Lock locks rw for writing.
func (rw *RWMutex) Lock() { rw.mu.Lock() }

// Unlock unlocks rw for writing.
func (rw *RWMutex) Unlock() { rw.mu.Unlock() }

// RLock locks rw for reading. It is exclusive here; see the type comment.
func (rw *RWMutex) RLock() { rw.mu.Lock() }

// RUnlock undoes a single RLock call.
func (rw *RWMutex) RUnlock() { rw.mu.Unlock() }

// TryLock tries to lock rw for writing and reports whether it succeeded.
func (rw *RWMutex) TryLock() bool { return rw.mu.TryLock() }

// TryRLock tries to lock rw for reading and reports whether it succeeded.
func (rw *RWMutex) TryRLock() bool { return rw.mu.TryLock() }
