//go:build tinygo

package syncx

import (
	"sync"
	"testing"
	"time"
)

// TestUpstreamRWMutexStillDeadlocks pins the reason this package exists. It
// runs the four-step interleaving against TinyGo's own sync.RWMutex and
// expects the deadlock. The day a TinyGo release ships the fix
// (tinygo-org/tinygo#5630) this test fails, which is the signal to delete the
// tinygo shim and make RWMutex an alias on every build.
//
// The two goroutines it leaves behind are parked on a local lock; the test
// binary exits over them.
func TestUpstreamRWMutexStillDeadlocks(t *testing.T) {
	var mu sync.RWMutex
	if fourStep(&mu, 2*time.Second) {
		t.Errorf("TinyGo's sync.RWMutex no longer deadlocks on the four-step interleaving: " +
			"retire internal/syncx's tinygo shim (rwmutex_tinygo.go) and alias sync.RWMutex everywhere")
	}
}
