package cbor

import "testing"

// testingAllocs reports the average allocations per run of f, which is what
// pins the steady-state properties this package promises.
func testingAllocs(f func()) float64 {
	return testing.AllocsPerRun(50, f)
}

// The two profiles ebigentserver's catalog defines, written out here as struct
// literals. They live in this package's tests rather than in its API because
// they are that project's restrictions, not this package's: cbor supplies the
// mechanism and every consumer names its own subset.
//
// This is also the worked example the README points at.
var (
	wireProfile = Profile{
		Name:             "wire",
		RejectMaps:       true,
		RejectTags:       true,
		RejectFloats:     true,
		RejectIndefinite: true,
		RejectTextKeys:   true,
	}

	worldProfile = Profile{
		Name:              "world",
		RequireSortedKeys: true,
		KeyOrder:          BytewiseKeyOrder,
		RejectFloats:      true,
		RejectIndefinite:  true,
	}

	// worldWithFloats is the same subset with the simulation rule dropped,
	// which is what a non-game consumer of the evolvable shape would want.
	worldWithFloats = Profile{
		Name:              "world",
		RequireSortedKeys: true,
		KeyOrder:          BytewiseKeyOrder,
		RejectIndefinite:  true,
	}
)

// defaultOpts is the limit set a caller gets by leaving DecoderOptions alone.
func defaultOpts() DecoderOptions { return DecoderOptions{} }
