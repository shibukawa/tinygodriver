//go:build !tinygo && !force_tinygo_logic

package pgxpool

import (
	ppool "github.com/jackc/pgx/v5/pgxpool"
)

// On this build the re-exported names must BE the upstream pgxpool types, not
// lookalikes; see database/pgx/compat_std_test.go for why these are
// compile-time assertions.
var (
	_ *ppool.Pool            = (*Pool)(nil)
	_ *ppool.Conn            = (*Conn)(nil)
	_ *ppool.Config          = (*Config)(nil)
	_ ppool.Stat             = Stat{}
	_ ppool.Tx               = Tx{}
	_ ppool.ShouldPingParams = ShouldPingParams{}
	_ ppool.AcquireTracer    = AcquireTracer(nil)
	_ ppool.ReleaseTracer    = ReleaseTracer(nil)
)
