//go:build (tinygo || bytealg_c) && cgo && (arm64 || amd64) && (darwin || linux || windows)

#include <stdint.h>
#include <stddef.h>

typedef uint8_t u8x16 __attribute__((vector_size(16)));
typedef uint32_t u32x4 __attribute__((vector_size(16)));

static inline u8x16 load16(const uint8_t *p) {
    u8x16 v;
    __builtin_memcpy(&v, p, 16);
    return v;
}
static inline void store16(uint8_t *p, u8x16 v) { __builtin_memcpy(p, &v, 16); }
static inline u8x16 splat(uint8_t c) {
    return (u8x16){c,c,c,c,c,c,c,c,c,c,c,c,c,c,c,c};
}
static inline int any(u8x16 v) {
#if defined(__aarch64__)
    return __builtin_reduce_max(v) != 0;
#else
    unsigned mask;
    __asm__("pmovmskb %1, %0" : "=r"(mask) : "x"(v));
    return mask != 0;
#endif
}

size_t tinygodriver_mask(uint8_t *p, size_t len, uint32_t key) {
    u8x16 k = (u8x16)(u32x4){key,key,key,key};
    size_t i = 0;
    for (; len-i >= 64; i += 64) {
        store16(p+i, load16(p+i)^k);
        store16(p+i+16, load16(p+i+16)^k);
        store16(p+i+32, load16(p+i+32)^k);
        store16(p+i+48, load16(p+i+48)^k);
    }
    for (; len-i >= 16; i += 16) store16(p+i, load16(p+i)^k);
    return i;
}

ptrdiff_t tinygodriver_index_byte(const uint8_t *p, size_t len, uint8_t c) {
    u8x16 needle = splat(c);
    size_t i = 0;
    for (; len-i >= 16; i += 16) {
        if (any((u8x16)(load16(p+i) == needle))) {
            for (size_t j = 0; j < 16; j++) if (p[i+j] == c) return i+j;
        }
    }
    for (; i < len; i++) if (p[i] == c) return i;
    return -1;
}

int tinygodriver_header_value(const uint8_t *p, size_t len) {
    u8x16 space = splat(32), tab = splat(9), del = splat(127);
    size_t i = 0;
    for (; len-i >= 16; i += 16) {
        u8x16 v = load16(p+i);
        if (any((u8x16)((v < space) & (v != tab)) | (u8x16)(v == del))) return 0;
    }
    for (; i < len; i++) if ((p[i] < 32 && p[i] != 9) || p[i] == 127) return 0;
    return 1;
}
