package syncx

import (
	"sync"
	"testing"
	"time"
)

// fourStep is the interleaving that deadlocks TinyGo's sync.RWMutex:
//
//  1. R1 holds the read lock.
//  2. W calls Lock and waits for the readers to drain.
//  3. R2 calls RLock while W is waiting.
//  4. R1 releases.
//
// A correct RWMutex hands the lock to W, then to R2. TinyGo's counts R2's
// arrival as a reader that never leaves, so W waits for R2 and R2 waits for
// W. The function reports whether both finished within the bound.
type rwLocker interface {
	Lock()
	Unlock()
	RLock()
	RUnlock()
}

func fourStep(mu rwLocker, bound time.Duration) bool {
	var wg sync.WaitGroup
	mu.RLock() // 1
	wg.Add(1)
	go func() { // 2
		defer wg.Done()
		mu.Lock()
		mu.Unlock()
	}()
	time.Sleep(50 * time.Millisecond)
	wg.Add(1)
	go func() { // 3
		defer wg.Done()
		mu.RLock()
		mu.RUnlock()
	}()
	time.Sleep(50 * time.Millisecond)
	mu.RUnlock() // 4

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
		return true
	case <-time.After(bound):
		return false
	}
}

// TestRWMutexHandsOverToWaitingWriter is the regression test for the shim: on
// every compiler, the four-step interleaving must complete.
func TestRWMutexHandsOverToWaitingWriter(t *testing.T) {
	var mu RWMutex
	if !fourStep(&mu, 3*time.Second) {
		t.Fatalf("syncx.RWMutex deadlocked: writer and late reader still waiting after 3s")
		return
	}
}

// TestRWMutexStress is the shape of netdev.Device.mu under sixteen concurrent
// connections: readers hammer a map lookup while a writer inserts. Under
// TinyGo the standard RWMutex hung this in 13 of 13 runs on 2026-09-02.
func TestRWMutexStress(t *testing.T) {
	var mu RWMutex
	m := map[int]int{}
	var wg sync.WaitGroup
	const readers, readIters, writeIters = 16, 5000, 1000
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := 0
			for j := 0; j < readIters; j++ {
				mu.RLock()
				s += m[j&63]
				mu.RUnlock()
			}
			_ = s
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < writeIters; j++ {
			mu.Lock()
			m[j&63] = j
			mu.Unlock()
		}
	}()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatalf("syncx.RWMutex stress did not finish within 20s")
		return
	}
}
