//go:build goexperiment.simd && arm64 && !tinygo && !bytealg_c

package bytealg

import "simd/archsimd"

const Available = true // NEON is part of the arm64 baseline.

func anyNonzero(x archsimd.Uint8x16) bool { return x.ReduceMax() != 0 }
