//go:build (tinygo || bytealg_c) && cgo && (arm64 || amd64) && (darwin || linux || windows)

package bytealg

/*
#include <stddef.h>
#include <stdint.h>
size_t tinygodriver_mask(uint8_t *p, size_t len, uint32_t key);
ptrdiff_t tinygodriver_index_byte(const uint8_t *p, size_t len, uint8_t c);
int tinygodriver_header_value(const uint8_t *p, size_t len);
*/
import "C"

import (
	"bytes"
	"unsafe"
)

const Available = true // NEON on arm64; SSE2 on amd64.

func maskVector(b []byte, key uint32) int {
	if len(b) == 0 {
		return 0
	}
	return int(C.tinygodriver_mask((*C.uint8_t)(unsafe.Pointer(&b[0])), C.size_t(len(b)), C.uint32_t(key)))
}

func headerVector(b []byte) bool {
	if len(b) == 0 {
		return true
	}
	return C.tinygodriver_header_value((*C.uint8_t)(unsafe.Pointer(&b[0])), C.size_t(len(b))) != 0
}

// IndexByte searches without reading past the slice's length.
func IndexByte(b []byte, c byte) int {
	if len(b) < 64 {
		return bytes.IndexByte(b, c)
	}
	return int(C.tinygodriver_index_byte((*C.uint8_t)(unsafe.Pointer(&b[0])), C.size_t(len(b)), C.uint8_t(c)))
}
