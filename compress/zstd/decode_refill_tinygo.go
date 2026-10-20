//go:build tinygo

package zstd

// LLVM benefits from replacing bytewise refill shifts with an overlapping
// 64-bit load. Keep the compiler choice static, outside the decoding loop.
const reloadBitWindow = true
