//go:build (tinygo || base64_c) && cgo && (arm64 || amd64) && (darwin || linux || windows)

package simdbase64

/*
#include <stddef.h>
#include <stdint.h>
int tinygodriver_base64_available(void);
typedef struct { size_t read; size_t written; } tinygodriver_base64_result;
tinygodriver_base64_result tinygodriver_base64_decode(uint8_t *dst, size_t dlen, const uint8_t *src, size_t slen, int url);
size_t tinygodriver_base64_encode(uint8_t *dst, size_t dlen, const uint8_t *src, size_t slen, int url);
*/
import "C"

import "unsafe"

var vectorAvailable = C.tinygodriver_base64_available() != 0

func decodeVector(dst, src []byte, url bool) (int, int) {
	if len(dst) < 16 || len(src) < 16 {
		return 0, 0
	}
	k := C.int(0)
	if url {
		k = 1
	}
	r := C.tinygodriver_base64_decode((*C.uint8_t)(unsafe.Pointer(&dst[0])), C.size_t(len(dst)), (*C.uint8_t)(unsafe.Pointer(&src[0])), C.size_t(len(src)), k)
	return int(r.read), int(r.written)
}

func encodeVector(dst, src []byte, url bool) int {
	if len(dst) < 16 || len(src) < 16 {
		return 0
	}
	k := C.int(0)
	if url {
		k = 1
	}
	return int(C.tinygodriver_base64_encode((*C.uint8_t)(unsafe.Pointer(&dst[0])), C.size_t(len(dst)), (*C.uint8_t)(unsafe.Pointer(&src[0])), C.size_t(len(src)), k))
}
