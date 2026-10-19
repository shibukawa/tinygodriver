//go:build (tinygo || base64_c) && cgo && (arm64 || amd64) && (darwin || linux || windows)

/* TinyGo does not lower Go's archsimd intrinsics. Use clang vector operations
 * instead. The two instruction wrappers below avoid depending on arm_neon.h
 * or x86 intrinsic headers missing from TinyGo's trimmed clang resources.
 * All loads/stores are bounded by the Go slices' lengths. */
#include <stdint.h>
#include <stddef.h>

typedef uint8_t u8x16 __attribute__((vector_size(16)));
typedef uint32_t u32x4 __attribute__((vector_size(16)));
typedef int8_t i8x16 __attribute__((vector_size(16)));
typedef struct { size_t read; size_t written; } tinygodriver_base64_result;

static inline u8x16 load16(const uint8_t *p) {
    u8x16 v;
    __builtin_memcpy(&v, p, 16);
    return v;
}
static inline void store16(uint8_t *p, u8x16 v) { __builtin_memcpy(p, &v, 16); }
static inline u8x16 splat8(uint8_t x) { return (u8x16){x,x,x,x,x,x,x,x,x,x,x,x,x,x,x,x}; }
static inline u32x4 splat32(uint32_t x) { return (u32x4){x,x,x,x}; }
static inline u8x16 lookup(u8x16 table, u8x16 indices) {
#if defined(__aarch64__)
    __asm__("tbl %0.16b, {%1.16b}, %2.16b" : "=w"(table) : "w"(table), "w"(indices));
#elif defined(__x86_64__)
    /* Only called after the SSSE3 check. Indices are 0..15 or 0xff. */
    __asm__("pshufb %1, %0" : "+x"(table) : "x"(indices));
#endif
    return table;
}
static inline int all_nonzero(u8x16 v) {
#if defined(__aarch64__)
    return __builtin_reduce_min(v) != 0;
#else
    u8x16 zero = v == splat8(0);
    unsigned mask;
    __asm__("pmovmskb %1, %0" : "=r"(mask) : "x"(zero));
    return mask == 0;
#endif
}

int tinygodriver_base64_available(void) {
#if defined(__aarch64__)
    return 1;
#else
    uint32_t a = 1, b, c, d;
    __asm__("cpuid" : "+a"(a), "=b"(b), "=c"(c), "=d"(d));
    return (c & (1u << 9)) != 0; /* SSSE3; no AVX OS-state requirement. */
#endif
}

tinygodriver_base64_result tinygodriver_base64_decode(uint8_t *dst, size_t dlen, const uint8_t *src, size_t slen, int url) {
    const u8x16 hi = {0,0,1,2,4,8,16,32,0,0,0,0,0,0,0,0};
    const u8x16 lo_std = {42,62,62,62,62,62,62,62,62,62,60,21,20,20,20,21};
    const u8x16 lo_url = {42,62,62,62,62,62,62,62,62,62,60,20,20,21,20,28};
    const u8x16 delta_std = {0,0,19,4,191,191,185,185};
    const u8x16 delta_url = {0,0,17,4,191,191,185,185};
    const u8x16 pack = {2,1,0,6,5,4,10,9,8,14,13,12,255,255,255,255};
    const u8x16 lo = url ? lo_url : lo_std;
    const u8x16 delta = url ? delta_url : delta_std;
#if defined(__aarch64__)
    const u8x16 select_a = {0,1,2,4,5,6,8,9,10,12,13,14,255,255,255,255};
    const u8x16 select_b = {1,2,3,5,6,7,9,10,11,13,14,15,255,255,255,255};
    const i8x16 shift_a = {2,4,6,2,4,6,2,4,6,2,4,6};
    const i8x16 shift_b = {-4,-2,0,-4,-2,0,-4,-2,0,-4,-2,0};
#endif
    const u8x16 tail4 = {0,0,0,0,0,0,0,0,0,0,0,0,1,1,1,1};
    const u8x16 tail8 = {0,0,0,0,0,0,0,0,1,1,1,1,1,1,1,1};
    const u8x16 tail12 = {0,0,0,0,1,1,1,1,1,1,1,1,1,1,1,1};
    size_t n = 0, written = 0;
    for (;;) {
        while (n < slen && (src[n] == '\r' || src[n] == '\n')) n++;
        if (slen-n < 16 || dlen < 16) break;
        u8x16 x = load16(src+n);
        u8x16 h = (x >> 4) & splat8(15);
        u8x16 valid = lookup(lo, x & splat8(15)) & lookup(hi, h);
        size_t digits = 16;
        if (!all_nonzero(valid)) {
            if (all_nonzero(valid | tail4)) digits = 12;
            else if (all_nonzero(valid | tail8)) digits = 8;
            else if (all_nonzero(valid | tail12)) digits = 4;
            else break;
        }
        u8x16 v = x + lookup(delta, h);
        if (url) v += ((u8x16)(x == splat8('_'))) & splat8(33);
        else v -= ((u8x16)(x == splat8('/'))) & splat8(3);
#if defined(__aarch64__)
        u8x16 a = lookup(v, select_a), b = lookup(v, select_b);
        __asm__("ushl %0.16b, %1.16b, %2.16b" : "=w"(a) : "w"(a), "w"(shift_a));
        __asm__("ushl %0.16b, %1.16b, %2.16b" : "=w"(b) : "w"(b), "w"(shift_b));
        u8x16 packed = a | b;
#else
        u32x4 w = (u32x4)v;
        u32x4 pairs = ((w & splat32(0x003f003f)) << 6) | ((w >> 8) & splat32(0x003f003f));
        u32x4 out = ((pairs & splat32(0x00000fff)) << 12) | (pairs >> 16);
        u8x16 packed = lookup((u8x16)out, pack);
#endif
        size_t produced = digits/4*3;
        if (digits == 16) store16(dst, packed);
        else {
            uint8_t scratch[16];
            store16(scratch, packed);
            __builtin_memcpy(dst, scratch, produced);
        }
        n += digits;
        written += produced;
        dst += produced;
        dlen -= produced;
    }
    return (tinygodriver_base64_result){n, written};
}

size_t tinygodriver_base64_encode(uint8_t *dst, size_t dlen, const uint8_t *src, size_t slen, int url) {
    const u8x16 expand = {2,1,0,255,5,4,3,255,8,7,6,255,11,10,9,255};
    const u8x16 offsets_std = {65,71,252,252,252,252,252,252,252,252,252,252,237,240};
    const u8x16 offsets_url = {65,71,252,252,252,252,252,252,252,252,252,252,239,32};
    const u8x16 offsets = url ? offsets_url : offsets_std;
#if defined(__aarch64__)
    const u8x16 select_a = {0,0,1,2,3,3,4,5,6,6,7,8,9,9,10,11};
    const u8x16 select_b = {255,1,2,255,255,4,5,255,255,7,8,255,255,10,11,255};
    const i8x16 shift_a = {-2,4,2,0,-2,4,2,0,-2,4,2,0,-2,4,2,0};
    const i8x16 shift_b = {0,-4,-6,0,0,-4,-6,0,0,-4,-6,0,0,-4,-6,0};
#endif
    size_t n = 0;
    while (slen-n >= 16 && dlen >= 16) {
#if defined(__aarch64__)
        u8x16 x = load16(src+n);
        u8x16 a = lookup(x, select_a), b = lookup(x, select_b);
        __asm__("ushl %0.16b, %1.16b, %2.16b" : "=w"(a) : "w"(a), "w"(shift_a));
        __asm__("ushl %0.16b, %1.16b, %2.16b" : "=w"(b) : "w"(b), "w"(shift_b));
        u8x16 v = (a | b) & splat8(63);
#else
        u32x4 x = (u32x4)lookup(load16(src+n), expand);
        u8x16 v = (u8x16)(((x >> 18) & splat32(0x0000003f)) | ((x >> 4) & splat32(0x00003f00)) |
                          ((x << 10) & splat32(0x003f0000)) | ((x << 24) & splat32(0x3f000000)));
#endif
        /* Saturating v-51, plus one when v>25, gives 14 offset classes. */
        u8x16 index = ((v - splat8(51)) & (u8x16)(v > splat8(51))) + ((u8x16)(v > splat8(25)) & splat8(1));
        store16(dst, v + lookup(offsets, index));
        n += 12;
        dst += 16;
        dlen -= 16;
    }
    return n;
}
