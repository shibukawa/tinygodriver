//go:build !tinygo && !force_tinygo_logic

package syncx

import "sync"

// RWMutex is the standard library's on standard Go, where it is correct.
type RWMutex = sync.RWMutex
