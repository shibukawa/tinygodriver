//go:build (tinygo || hex_c) && cgo && (arm64 || amd64) && (darwin || linux || windows)

#include <stddef.h>
#include <stdint.h>

typedef uint8_t u8x16 __attribute__((vector_size(16)));
static inline u8x16 load16(const uint8_t *p) {
    u8x16 v; __builtin_memcpy(&v,p,16); return v;
}
static inline void store16(uint8_t *p,u8x16 v) { __builtin_memcpy(p,&v,16); }
static inline u8x16 splat(uint8_t x) { return (u8x16){x,x,x,x,x,x,x,x,x,x,x,x,x,x,x,x}; }
static inline int all_valid(u8x16 v) {
#if defined(__aarch64__)
    return __builtin_reduce_min(v) != 0;
#else
    unsigned mask;
    __asm__("pmovmskb %1, %0" : "=r"(mask) : "x"(v));
    return mask == 0xffff;
#endif
}

size_t tinygodriver_hex_encode(uint8_t *dst, size_t dlen, const uint8_t *src, size_t slen) {
    size_t i=0;
    for (; slen-i >= 16 && dlen >= 32; i+=16, dst+=32, dlen-=32) {
        u8x16 x=load16(src+i), hi=x>>4, lo=x&splat(15);
        hi += splat('0') + ((u8x16)(hi > splat(9)) & splat(39));
        lo += splat('0') + ((u8x16)(lo > splat(9)) & splat(39));
        store16(dst,__builtin_shufflevector(hi,lo,0,16,1,17,2,18,3,19,4,20,5,21,6,22,7,23));
        store16(dst+16,__builtin_shufflevector(hi,lo,8,24,9,25,10,26,11,27,12,28,13,29,14,30,15,31));
    }
    return i;
}

size_t tinygodriver_hex_decode(uint8_t *dst, size_t dlen, const uint8_t *src, size_t slen) {
    size_t i=0;
    for (; slen-i >= 32 && dlen >= 16; i+=32,dst+=16,dlen-=16) {
        u8x16 x=load16(src+i), y=load16(src+i+16);
        u8x16 dx=x-splat('0'), dy=y-splat('0');
        u8x16 ax=(u8x16)(((x|splat(32))-splat('a'))<=splat(5));
        u8x16 ay=(u8x16)(((y|splat(32))-splat('a'))<=splat(5));
        u8x16 vx=(u8x16)(dx<=splat(9))|ax, vy=(u8x16)(dy<=splat(9))|ay;
        if (!all_valid(vx&vy)) break;
        dx = (x&splat(15))+(ax&splat(9)); dy = (y&splat(15))+(ay&splat(9));
        u8x16 hi=__builtin_shufflevector(dx,dy,0,2,4,6,8,10,12,14,16,18,20,22,24,26,28,30);
        u8x16 lo=__builtin_shufflevector(dx,dy,1,3,5,7,9,11,13,15,17,19,21,23,25,27,29,31);
        store16(dst,(hi<<4)|lo);
    }
    return i;
}
