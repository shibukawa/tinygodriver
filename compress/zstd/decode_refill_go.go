//go:build force_tinygo_logic && !tinygo

package zstd

// Go inlines the compact refill into its entropy loops. The larger reload
// version exceeds its inlining budget and slows sequence-heavy blocks.
const reloadBitWindow = false
