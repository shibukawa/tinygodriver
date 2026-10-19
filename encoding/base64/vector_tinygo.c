//go:build (tinygo || base64_c) && cgo && (arm64 || amd64) && (darwin || linux || windows)

/* TinyGo does not lower Go's archsimd intrinsics. Use clang vector operations
 * instead. The instruction wrappers below avoid depending on arm_neon.h
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

static inline u8x16 min16(u8x16 a, u8x16 b) { return __builtin_elementwise_min(a, b); }

/* TinyGo's default -Oz otherwise outlines this helper and calls it for
 * every vector, serializing the four independent lanes of the bulk loop. */
static inline __attribute__((always_inline)) u8x16 decode_pack(u8x16 v) {
#if defined(__aarch64__)
    const u8x16 select_a = {0,1,2,4,5,6,8,9,10,12,13,14,255,255,255,255};
    const u8x16 select_b = {1,2,3,5,6,7,9,10,11,13,14,15,255,255,255,255};
    const i8x16 shift_a = {2,4,6,2,4,6,2,4,6,2,4,6};
    const i8x16 shift_b = {-4,-2,0,-4,-2,0,-4,-2,0,-4,-2,0};
    u8x16 a = lookup(v, select_a), b = lookup(v, select_b);
    __asm__("ushl %0.16b, %1.16b, %2.16b" : "=w"(a) : "w"(a), "w"(shift_a));
    __asm__("ushl %0.16b, %1.16b, %2.16b" : "=w"(b) : "w"(b), "w"(shift_b));
    return a | b;
#else
    const u8x16 pack = {2,1,0,6,5,4,10,9,8,14,13,12,255,255,255,255};
    u32x4 w = (u32x4)v;
    u32x4 pairs = ((w & splat32(0x003f003f)) << 6) | ((w >> 8) & splat32(0x003f003f));
    u32x4 out = ((pairs & splat32(0x00000fff)) << 12) | (pairs >> 16);
    return lookup((u8x16)out, pack);
#endif
}

typedef struct { u8x16 val[4]; } decode_block64;

/* ARM's structured load separates the four characters of every quantum.
 * The NEON builtin type discriminator 48 selects 128-bit unsigned bytes,
 * avoiding a dependency on TinyGo's trimmed arm_neon.h headers. */
static inline __attribute__((always_inline)) decode_block64 decode_load64(const uint8_t *src) {
    decode_block64 v;
#if defined(__aarch64__)
    __builtin_neon_vld4q_v(&v, src, 48);
#else
    v.val[0] = load16(src); v.val[1] = load16(src+16);
    v.val[2] = load16(src+32); v.val[3] = load16(src+48);
#endif
    return v;
}

static inline __attribute__((always_inline)) void decode_store48(uint8_t *dst, u8x16 a, u8x16 b, u8x16 c, u8x16 d) {
#if defined(__aarch64__)
    /* Merge each quantum with immediate shifts, then interleave its three
     * decoded bytes directly into an exact 48-byte output block. */
    u8x16 out0 = (u8x16)__builtin_neon_vsliq_n_v((i8x16)(b >> 4), (i8x16)a, 2, 48);
    u8x16 out1 = (u8x16)__builtin_neon_vsliq_n_v((i8x16)(c >> 2), (i8x16)b, 4, 48);
    u8x16 out2 = (u8x16)__builtin_neon_vsliq_n_v((i8x16)d, (i8x16)c, 6, 48);
    __builtin_neon_vst3q_v(dst, (i8x16)out0, (i8x16)out1, (i8x16)out2, 48);
#else
    /* The x86 packer retains four scratch bytes after the 48-byte output. */
    store16(dst, decode_pack(a)); store16(dst+12, decode_pack(b));
    store16(dst+24, decode_pack(c)); store16(dst+36, decode_pack(d));
#endif
}

static inline u8x16 high_bits(u8x16 x) {
#if defined(__aarch64__)
    return x >> 3;
#else
    return (x >> 4) & splat8(15);
#endif
}

static inline u8x16 translate(u8x16 x, u8x16 h, u8x16 valid, u8x16 delta, u8x16 special, u8x16 correction) {
#if defined(__aarch64__)
    /* The class bit from validation also selects the decoding offset. */
    __asm__("clz %0.16b, %1.16b" : "=w"(valid) : "w"(valid));
    return x + lookup(delta, valid);
#else
    return x + lookup(delta, h) + (((u8x16)(x == special)) & correction);
#endif
}

static inline void store9(uint8_t *dst, u8x16 v) {
    uint8_t scratch[16];
    store16(scratch, v);
    __builtin_memcpy(dst, scratch, 9);
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

/* Keep the fallback's constants and compact loop independent of bulk-kernel
 * register allocation. This preserves throughput on short/irregular lines. */
static inline __attribute__((aligned(64))) tinygodriver_base64_result decode_small(uint8_t *dst, size_t dlen, const uint8_t *src, size_t slen, int url) {
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
        /* Packing invalid lanes is harmless until their validity is known.
         * Keep full vectors on constant input/output strides; only fringes
         * need a dynamic digit count and bounded partial stores. */
        if (all_nonzero(valid)) {
            store16(dst, packed);
            n += 16; written += 12; dst += 12; dlen -= 12;
            continue;
        }
        size_t digits;
        if (all_nonzero(valid | tail4)) digits = 12;
        else if (all_nonzero(valid | tail8)) digits = 8;
        else if (all_nonzero(valid | tail12)) digits = 4;
        else break;
        size_t produced = digits/4*3;
        uint8_t scratch[16];
        store16(scratch, packed);
        /* Constant sizes stay inline under -Oz; a variable memcpy
         * forces a call and spills the vector constants around it. */
        if (digits == 12) __builtin_memcpy(dst, scratch, 9);
        else if (digits == 8) __builtin_memcpy(dst, scratch, 6);
        else __builtin_memcpy(dst, scratch, 3);
        n += digits;
        written += produced;
        dst += produced;
        dlen -= produced;
    }
    return (tinygodriver_base64_result){n, written};
}

tinygodriver_base64_result tinygodriver_base64_decode(uint8_t *dst, size_t dlen, const uint8_t *src, size_t slen, int url) {
    /* Small messages do not amortize two sets of vector constants. The
     * direct return also permits a tail call to the compact SIMD decoder. */
    if (slen < 256) return decode_small(dst, dlen, src, slen, url);
#if defined(__aarch64__)
    /* Classifier tables adapted from simdutf; see NOTICE. Clear control and
     * space classes, and reject standard punctuation in the URL alphabet. */
    const u8x16 hi_std = {0,0,0,0,0,6,8,8,16,32,32,16,64,128,128,64};
    const u8x16 hi_url = {0,0,0,0,0,4,8,8,16,32,32,18,64,128,128,64};
    const u8x16 hi = url ? hi_url : hi_std;
    const u8x16 lo_std = {169,248,248,248,248,248,248,248,248,249,241,162,161,161,160,164};
    const u8x16 lo_url = {169,248,248,248,248,248,248,248,248,249,241,160,161,165,160,162};
    const u8x16 delta_std = {185,185,191,191,4,16,19};
    const u8x16 delta_url = {185,185,191,191,4,17,224};
#else
    const u8x16 hi = {0,0,1,2,4,8,16,32,0,0,0,0,0,0,0,0};
    const u8x16 lo_std = {42,62,62,62,62,62,62,62,62,62,60,21,20,20,20,21};
    const u8x16 lo_url = {42,62,62,62,62,62,62,62,62,62,60,20,20,21,20,28};
    const u8x16 delta_std = {0,0,19,4,191,191,185,185};
    const u8x16 delta_url = {0,0,17,4,191,191,185,185};
#endif
    const u8x16 lo = url ? lo_url : lo_std;
    const u8x16 delta = url ? delta_url : delta_std;
    const u8x16 special = splat8(url ? '_' : '/');
    const u8x16 correction = splat8(url ? 33 : 253);
    const u8x16 tail4 = {0,0,0,0,0,0,0,0,0,0,0,0,1,1,1,1};
    size_t n = 0, written = 0;
    while (n < slen && (src[n] == '\r' || src[n] == '\n')) n++;
    /* Select the MIME loop once. Plain input does not pay a lookahead and
     * MIME branch on every 64-character block. Irregular lines leave this
     * loop untouched and use the plain/compact paths below. */
    while (slen-n >= 80 && dlen >= 57 && (src[n+76] == '\n' || src[n+76] == '\r')) {
        decode_block64 block = decode_load64(src+n);
        u8x16 x0 = block.val[0], x1 = block.val[1], x2 = block.val[2], x3 = block.val[3];
        u8x16 x4 = load16(src+n+64);
        u8x16 h0 = high_bits(x0), h1 = high_bits(x1);
        u8x16 h2 = high_bits(x2), h3 = high_bits(x3), h4 = high_bits(x4);
        u8x16 v0 = lookup(lo, x0 & splat8(15)) & lookup(hi, h0);
        u8x16 v1 = lookup(lo, x1 & splat8(15)) & lookup(hi, h1);
        u8x16 v2 = lookup(lo, x2 & splat8(15)) & lookup(hi, h2);
        u8x16 v3 = lookup(lo, x3 & splat8(15)) & lookup(hi, h3);
        u8x16 v4 = lookup(lo, x4 & splat8(15)) & lookup(hi, h4);
        if (!all_nonzero(min16(min16(v0, v1), min16(min16(v2, v3), v4 | tail4)))) break;
        decode_store48(dst,
            translate(x0, h0, v0, delta, special, correction),
            translate(x1, h1, v1, delta, special, correction),
            translate(x2, h2, v2, delta, special, correction),
            translate(x3, h3, v3, delta, special, correction));
        u8x16 p4 = decode_pack(translate(x4, h4, v4, delta, special, correction));
        store9(dst+48, p4);
        n += 77;
        if (src[n-1] == '\r' && src[n] == '\n') n++;
        written += 57; dst += 57; dlen -= 57;
        continue;
    }
    while (slen-n >= 64 && dlen >= 52) {
        if (src[n] == '\r' || src[n] == '\n') {
            do { n++; } while (n < slen && (src[n] == '\r' || src[n] == '\n'));
            continue;
        }
        /* Four vectors share one reduction/branch. Rejected batches leave
         * output untouched and are retried by the single-vector path. */
        decode_block64 block = decode_load64(src+n);
        u8x16 x0 = block.val[0], x1 = block.val[1], x2 = block.val[2], x3 = block.val[3];
        u8x16 h0 = high_bits(x0), h1 = high_bits(x1);
        u8x16 h2 = high_bits(x2), h3 = high_bits(x3);
        u8x16 v0 = lookup(lo, x0 & splat8(15)) & lookup(hi, h0);
        u8x16 v1 = lookup(lo, x1 & splat8(15)) & lookup(hi, h1);
        u8x16 v2 = lookup(lo, x2 & splat8(15)) & lookup(hi, h2);
        u8x16 v3 = lookup(lo, x3 & splat8(15)) & lookup(hi, h3);
        if (all_nonzero(min16(min16(v0, v1), min16(v2, v3)))) {
            decode_store48(dst,
                translate(x0, h0, v0, delta, special, correction),
                translate(x1, h1, v1, delta, special, correction),
                translate(x2, h2, v2, delta, special, correction),
                translate(x3, h3, v3, delta, special, correction));
            n += 64; written += 48; dst += 48; dlen -= 48;
            continue;
        }
        /* No more bulk-loop overhead for short or irregular lines. */
        break;
    }
    tinygodriver_base64_result tail = decode_small(dst, dlen, src+n, slen-n, url);
    tail.read += n;
    tail.written += written;
    return tail;
}

static inline size_t encode_small(uint8_t *dst, size_t dlen, const uint8_t *src, size_t slen, int url) {
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

#if defined(__aarch64__)
/* These Clang builtins use the unsigned-byte type tag 48 from arm_neon.h.
 * Calling them directly keeps the structured loads/stores and four-register
 * table lookup available with TinyGo's trimmed, header-free clang resources. */
static inline __attribute__((always_inline)) u8x16 encode_lookup64(u8x16 t0, u8x16 t1, u8x16 t2, u8x16 t3, u8x16 v) {
    return (u8x16)__builtin_neon_vqtbl4q_v((i8x16)t0, (i8x16)t1, (i8x16)t2, (i8x16)t3, (i8x16)v, 48);
}

/* Keep alphabet-table setup out of short messages. The public wrapper can
 * tail-call this function, leaving the original compact encoder a leaf. */
static __attribute__((noinline)) size_t encode_large(uint8_t *dst, size_t dlen, const uint8_t *src, size_t slen, int url) {
    const u8x16 t0 = {'A','B','C','D','E','F','G','H','I','J','K','L','M','N','O','P'};
    const u8x16 t1 = {'Q','R','S','T','U','V','W','X','Y','Z','a','b','c','d','e','f'};
    const u8x16 t2 = {'g','h','i','j','k','l','m','n','o','p','q','r','s','t','u','v'};
    const u8x16 t3_std = {'w','x','y','z','0','1','2','3','4','5','6','7','8','9','+','/'};
    const u8x16 t3_url = {'w','x','y','z','0','1','2','3','4','5','6','7','8','9','-','_'};
    const u8x16 t3 = url ? t3_url : t3_std;
    size_t n = 0;
    while (slen-n >= 48 && dlen >= 64) {
        /* LD3 deinterleaves sixteen triples before either store. Its exact
         * 48-byte read also eliminates the compact encoder's load overlap. */
        struct { u8x16 val[3]; } x;
        __builtin_neon_vld3q_v(&x, src+n, 48);
        u8x16 a = x.val[0] >> 2;
        u8x16 b = ((x.val[0] << 4) | (x.val[1] >> 4)) & splat8(63);
        u8x16 c = ((x.val[1] << 2) | (x.val[2] >> 6)) & splat8(63);
        u8x16 d = x.val[2] & splat8(63);
        a = encode_lookup64(t0, t1, t2, t3, a);
        b = encode_lookup64(t0, t1, t2, t3, b);
        c = encode_lookup64(t0, t1, t2, t3, c);
        d = encode_lookup64(t0, t1, t2, t3, d);
        __builtin_neon_vst4q_v(dst, (i8x16)a, (i8x16)b, (i8x16)c, (i8x16)d, 48);
        n += 48;
        dst += 64;
        dlen -= 64;
    }
    return n + encode_small(dst, dlen, src+n, slen-n, url);
}
#endif

size_t tinygodriver_base64_encode(uint8_t *dst, size_t dlen, const uint8_t *src, size_t slen, int url) {
#if defined(__aarch64__)
    if (slen >= 256) return encode_large(dst, dlen, src, slen, url);
#endif
    return encode_small(dst, dlen, src, slen, url);
}
