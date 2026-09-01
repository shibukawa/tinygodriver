/*
 * Minimal aarch64 NEON intrinsics for mbedTLS AES and GHASH acceleration.
 *
 * TinyGo ships a trimmed clang resource directory that omits arm_neon.h,
 * because that header is generated during an LLVM build rather than checked
 * into the clang source tree. Vendoring the real 3.2 MB header does not work
 * either: it is written against one exact clang version's __builtin_neon_*
 * signatures, and clang 21's copy failed to compile with the clang 20.1.1 that
 * TinyGo 0.41 bundled. The bundled compiler keeps moving — TinyGo 0.42 is on
 * LLVM 22.1.4 — so pinning to any one copy is the wrong shape.
 *
 * This header therefore declares only what mbedTLS uses, and it expresses the
 * crypto instructions as inline assembly rather than compiler builtins.
 * Instruction mnemonics are architectural and stable, so this keeps working
 * across TinyGo and LLVM upgrades.
 *
 * It is selected only when MBEDTLS_TINYGO_NEON is defined, which the TinyGo
 * build sets. A host Go build has a complete toolchain and uses the real
 * <arm_neon.h>; the types below rely on clang-specific attributes that GCC
 * does not accept.
 *
 * Correctness is not taken on trust: MBEDTLS_SELF_TEST is enabled and the AES
 * and GCM NIST known-answer vectors run against this code.
 */
#ifndef TINYGODRIVER_TINYGO_ARM_NEON_H
#define TINYGODRIVER_TINYGO_ARM_NEON_H

#if !defined(__aarch64__)
#error "this minimal arm_neon.h supports aarch64 only"
#endif

#include <stdint.h>

/* Vector types, declared with the same clang attributes the real header uses
 * so that casts between them behave identically. */
typedef uint8_t __attribute__((neon_vector_type(8))) uint8x8_t;
typedef uint8_t __attribute__((neon_vector_type(16))) uint8x16_t;
typedef uint32_t __attribute__((neon_vector_type(4))) uint32x4_t;
typedef uint64_t __attribute__((neon_vector_type(1))) uint64x1_t;
typedef uint64_t __attribute__((neon_vector_type(2))) uint64x2_t;

typedef uint64_t poly64_t;
typedef __uint128_t poly128_t;
typedef poly64_t __attribute__((neon_polyvector_type(1))) poly64x1_t;
typedef poly64_t __attribute__((neon_polyvector_type(2))) poly64x2_t;

typedef struct uint8x16x3_t {
    uint8x16_t val[3];
} uint8x16x3_t;

#define TINYGO_NEON_FN static __inline__ __attribute__((__always_inline__, __nodebug__))

/* ---------------------------------------------------------------- loads */

TINYGO_NEON_FN uint8x16_t vld1q_u8(const uint8_t *p)
{
    uint8x16_t r;
    __builtin_memcpy(&r, p, 16);
    return r;
}

TINYGO_NEON_FN void vst1q_u8(uint8_t *p, uint8x16_t v)
{
    __builtin_memcpy(p, &v, 16);
}

/* ------------------------------------------------------------ AES rounds */

/* aese performs AddRoundKey, SubBytes and ShiftRows. */
TINYGO_NEON_FN uint8x16_t vaeseq_u8(uint8x16_t data, uint8x16_t key)
{
    __asm__("aese %0.16b, %1.16b" : "+w"(data) : "w"(key));
    return data;
}

/* aesd is the inverse round. */
TINYGO_NEON_FN uint8x16_t vaesdq_u8(uint8x16_t data, uint8x16_t key)
{
    __asm__("aesd %0.16b, %1.16b" : "+w"(data) : "w"(key));
    return data;
}

TINYGO_NEON_FN uint8x16_t vaesmcq_u8(uint8x16_t data)
{
    uint8x16_t r;
    __asm__("aesmc %0.16b, %1.16b" : "=w"(r) : "w"(data));
    return r;
}

TINYGO_NEON_FN uint8x16_t vaesimcq_u8(uint8x16_t data)
{
    uint8x16_t r;
    __asm__("aesimc %0.16b, %1.16b" : "=w"(r) : "w"(data));
    return r;
}

/* ------------------------------------------------- GHASH polynomial mul */

TINYGO_NEON_FN poly128_t vmull_p64(poly64_t a, poly64_t b)
{
    poly128_t r;
    __asm__("pmull %0.1q, %1.1d, %2.1d" : "=w"(r) : "w"(a), "w"(b));
    return r;
}

TINYGO_NEON_FN poly128_t vmull_high_p64(poly64x2_t a, poly64x2_t b)
{
    poly128_t r;
    __asm__("pmull2 %0.1q, %1.2d, %2.2d" : "=w"(r) : "w"(a), "w"(b));
    return r;
}

/* Reverse the bit order within every byte. */
TINYGO_NEON_FN uint8x16_t vrbitq_u8(uint8x16_t x)
{
    uint8x16_t r;
    __asm__("rbit %0.16b, %1.16b" : "=w"(r) : "w"(x));
    return r;
}

/* ------------------------------------------------------ generic vector ops */

TINYGO_NEON_FN uint8x16_t veorq_u8(uint8x16_t a, uint8x16_t b)
{
    return a ^ b;
}

TINYGO_NEON_FN uint8x16_t vdupq_n_u8(uint8_t v)
{
    return (uint8x16_t) {v, v, v, v, v, v, v, v, v, v, v, v, v, v, v, v};
}

TINYGO_NEON_FN uint32x4_t vdupq_n_u32(uint32_t v)
{
    return (uint32x4_t) {v, v, v, v};
}

TINYGO_NEON_FN uint64x1_t vget_low_u64(uint64x2_t a)
{
    return (uint64x1_t) __builtin_shufflevector(a, a, 0);
}

TINYGO_NEON_FN uint64x1_t vget_high_u64(uint64x2_t a)
{
    return (uint64x1_t) __builtin_shufflevector(a, a, 1);
}

TINYGO_NEON_FN poly64x1_t vget_low_p64(poly64x2_t a)
{
    return (poly64x1_t) __builtin_shufflevector(a, a, 0);
}

/* n must be a constant expression, as it is in aesce.c. */
#define vgetq_lane_u32(v, n) ((v)[(n)])
#define vshrq_n_u64(v, n)    ((uint64x2_t) ((v) >> (n)))
#define vextq_u8(a, b, n)                                                 \
    ((uint8x16_t) __builtin_shufflevector(                                \
         (a), (b), (n) + 0, (n) + 1, (n) + 2, (n) + 3, (n) + 4, (n) + 5,  \
         (n) + 6, (n) + 7, (n) + 8, (n) + 9, (n) + 10, (n) + 11,          \
         (n) + 12, (n) + 13, (n) + 14, (n) + 15))

/* ------------------------------------------------ SHA-256 (FEAT_SHA2) */

TINYGO_NEON_FN uint32x4_t vld1q_u32(const uint32_t *p)
{
    uint32x4_t r;
    __builtin_memcpy(&r, p, 16);
    return r;
}

TINYGO_NEON_FN void vst1q_u32(uint32_t *p, uint32x4_t v)
{
    __builtin_memcpy(p, &v, 16);
}

TINYGO_NEON_FN uint32x4_t vaddq_u32(uint32x4_t a, uint32x4_t b)
{
    return a + b;
}

/* Byte-swap within each 32-bit lane. */
TINYGO_NEON_FN uint8x16_t vrev32q_u8(uint8x16_t x)
{
    uint8x16_t r;
    __asm__("rev32 %0.16b, %1.16b" : "=w"(r) : "w"(x));
    return r;
}

/* SHA256H  Qd, Qn, Vm.4S */
TINYGO_NEON_FN uint32x4_t vsha256hq_u32(uint32x4_t abcd, uint32x4_t efgh,
                                        uint32x4_t wk)
{
    __asm__("sha256h %q0, %q1, %2.4s" : "+w"(abcd) : "w"(efgh), "w"(wk));
    return abcd;
}

/* SHA256H2 Qd, Qn, Vm.4S */
TINYGO_NEON_FN uint32x4_t vsha256h2q_u32(uint32x4_t efgh, uint32x4_t abcd,
                                         uint32x4_t wk)
{
    __asm__("sha256h2 %q0, %q1, %2.4s" : "+w"(efgh) : "w"(abcd), "w"(wk));
    return efgh;
}

/* SHA256SU0 Vd.4S, Vn.4S */
TINYGO_NEON_FN uint32x4_t vsha256su0q_u32(uint32x4_t w0_3, uint32x4_t w4_7)
{
    __asm__("sha256su0 %0.4s, %1.4s" : "+w"(w0_3) : "w"(w4_7));
    return w0_3;
}

/* SHA256SU1 Vd.4S, Vn.4S, Vm.4S */
TINYGO_NEON_FN uint32x4_t vsha256su1q_u32(uint32x4_t tw0_3, uint32x4_t w8_11,
                                          uint32x4_t w12_15)
{
    __asm__("sha256su1 %0.4s, %1.4s, %2.4s"
            : "+w"(tw0_3)
            : "w"(w8_11), "w"(w12_15));
    return tw0_3;
}

/* ---------------------------------------------- SHA-512 (FEAT_SHA512) */

TINYGO_NEON_FN uint64x2_t vld1q_u64(const uint64_t *p)
{
    uint64x2_t r;
    __builtin_memcpy(&r, p, 16);
    return r;
}

TINYGO_NEON_FN void vst1q_u64(uint64_t *p, uint64x2_t v)
{
    __builtin_memcpy(p, &v, 16);
}

TINYGO_NEON_FN uint64x2_t vaddq_u64(uint64x2_t a, uint64x2_t b)
{
    return a + b;
}

/* Byte-swap within each 64-bit lane. */
TINYGO_NEON_FN uint8x16_t vrev64q_u8(uint8x16_t x)
{
    uint8x16_t r;
    __asm__("rev64 %0.16b, %1.16b" : "=w"(r) : "w"(x));
    return r;
}

/* SHA512H  Qd, Qn, Vm.2D */
TINYGO_NEON_FN uint64x2_t vsha512hq_u64(uint64x2_t x, uint64x2_t y,
                                        uint64x2_t z)
{
    __asm__("sha512h %q0, %q1, %2.2d" : "+w"(x) : "w"(y), "w"(z));
    return x;
}

/* SHA512H2 Qd, Qn, Vm.2D */
TINYGO_NEON_FN uint64x2_t vsha512h2q_u64(uint64x2_t x, uint64x2_t y,
                                         uint64x2_t z)
{
    __asm__("sha512h2 %q0, %q1, %2.2d" : "+w"(x) : "w"(y), "w"(z));
    return x;
}

/* SHA512SU0 Vd.2D, Vn.2D */
TINYGO_NEON_FN uint64x2_t vsha512su0q_u64(uint64x2_t x, uint64x2_t y)
{
    __asm__("sha512su0 %0.2d, %1.2d" : "+w"(x) : "w"(y));
    return x;
}

/* SHA512SU1 Vd.2D, Vn.2D, Vm.2D */
TINYGO_NEON_FN uint64x2_t vsha512su1q_u64(uint64x2_t x, uint64x2_t y,
                                          uint64x2_t z)
{
    __asm__("sha512su1 %0.2d, %1.2d, %2.2d" : "+w"(x) : "w"(y), "w"(z));
    return x;
}

/* n must be a constant expression. */
#define vextq_u64(a, b, n) \
    ((uint64x2_t) __builtin_shufflevector((a), (b), (n) + 0, (n) + 1))

/* Reinterprets are pure bit casts. */
#define vreinterpretq_p64_u8(a)  ((poly64x2_t) (a))
#define vreinterpretq_u8_p128(a) ((uint8x16_t) (a))
#define vreinterpretq_u8_p64(a)  ((uint8x16_t) (a))
#define vreinterpretq_u64_p64(a) ((uint64x2_t) (a))
#define vreinterpretq_u32_u8(a)  ((uint32x4_t) (a))
#define vreinterpretq_u64_u8(a)  ((uint64x2_t) (a))
#define vreinterpretq_u8_u32(a)  ((uint8x16_t) (a))
#define vreinterpretq_u8_u64(a)  ((uint8x16_t) (a))

#endif /* TINYGODRIVER_TINYGO_ARM_NEON_H */
