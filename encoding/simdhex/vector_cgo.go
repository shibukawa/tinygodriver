//go:build (tinygo || hex_c) && cgo && (arm64 || amd64) && (darwin || linux || windows)

package simdhex

/*
#include <stddef.h>
#include <stdint.h>
size_t tinygodriver_hex_encode(uint8_t *dst, size_t dlen, const uint8_t *src, size_t slen);
size_t tinygodriver_hex_decode(uint8_t *dst, size_t dlen, const uint8_t *src, size_t slen);
*/
import "C"

import "unsafe"

const vectorAvailable = true // NEON on arm64; baseline SSE2 on amd64.

func encodeVector(dst, src []byte) int {
	if len(dst) < 32 || len(src) < 16 {
		return 0
	}
	return int(C.tinygodriver_hex_encode((*C.uint8_t)(unsafe.Pointer(&dst[0])), C.size_t(len(dst)), (*C.uint8_t)(unsafe.Pointer(&src[0])), C.size_t(len(src))))
}

func decodeVector(dst, src []byte) int {
	if len(dst) < 16 || len(src) < 32 {
		return 0
	}
	return int(C.tinygodriver_hex_decode((*C.uint8_t)(unsafe.Pointer(&dst[0])), C.size_t(len(dst)), (*C.uint8_t)(unsafe.Pointer(&src[0])), C.size_t(len(src))))
}
