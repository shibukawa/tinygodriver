package cbor

import "testing"

// testingAllocs reports the average allocations per run of f, which is what
// pins the steady-state properties this package promises.
func testingAllocs(f func()) float64 {
	return testing.AllocsPerRun(50, f)
}
