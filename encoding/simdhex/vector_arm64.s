//go:build arm64 && !tinygo && !hex_c && !purego

#include "textflag.h"

// Go-assembler NEON kernels. Each function computes its block count from both
// slice lengths before touching memory: the encoder reads 16 source bytes per
// 32 destination bytes, the decoder 32 per 16, so no load or store crosses a
// slice's length. Decoded output never overtakes unread input, which keeps
// in-place decoding safe. Neither function uses a stack frame or calls
// another function.

// Tables: the lowercase alphabet for the encoder's TBL, then the decoder's
// 64-entry value table, indexed by character minus '0'. Digits, uppercase and
// lowercase letters map to their value plus 16; every other index, including
// the out-of-range ones that TBL zero-fills, maps to zero.
DATA hexTablesNEON<>+0(SB)/8, $0x3736353433323130
DATA hexTablesNEON<>+8(SB)/8, $0x6665646362613938
DATA hexTablesNEON<>+16(SB)/8, $0x1716151413121110
DATA hexTablesNEON<>+24(SB)/8, $0x0000000000001918
DATA hexTablesNEON<>+32(SB)/8, $0x001f1e1d1c1b1a00
DATA hexTablesNEON<>+40(SB)/8, $0x0000000000000000
DATA hexTablesNEON<>+48(SB)/8, $0x0000000000000000
DATA hexTablesNEON<>+56(SB)/8, $0x0000000000000000
DATA hexTablesNEON<>+64(SB)/8, $0x001f1e1d1c1b1a00
DATA hexTablesNEON<>+72(SB)/8, $0x0000000000000000
GLOBL hexTablesNEON<>(SB), RODATA|NOPTR, $80

// V16 alphabet, V17 = 15. Split x into nibbles and look both up; ST2 then
// interleaves hi and lo into the output.
#define DIGITS(x, hi, lo) \
	VUSHR	$4, x, hi \
	VAND	V17.B16, x, lo \
	VTBL	hi, [V16.B16], hi \
	VTBL	lo, [V16.B16], lo

// func encodeNEON(dst, src []byte) int
TEXT ·encodeNEON(SB), NOSPLIT, $0-56
	MOVD	dst_len+8(FP), R1
	MOVD	src_len+32(FP), R3
	LSR	$5, R1, R1
	LSR	$4, R3, R3
	CMP	R1, R3
	CSEL	LT, R3, R1, R3
	LSL	$4, R3, R5
	MOVD	R5, ret+48(FP)
	CBZ	R3, encdone
	MOVD	dst_base+0(FP), R0
	MOVD	src_base+24(FP), R2
	MOVD	$hexTablesNEON<>(SB), R7
	VLD1	(R7), [V16.B16]
	VMOVI	$15, V17.B16
encode32:
	CMP	$2, R3
	BLT	encode16
	VLD1.P	32(R2), [V0.B16, V1.B16]
	DIGITS(V0.B16, V2.B16, V3.B16)
	DIGITS(V1.B16, V4.B16, V5.B16)
	VST2.P	[V2.B16, V3.B16], 32(R0)
	VST2.P	[V4.B16, V5.B16], 32(R0)
	SUB	$2, R3
	B	encode32
encode16:
	CBZ	R3, encdone
	VLD1	(R2), [V0.B16]
	DIGITS(V0.B16, V2.B16, V3.B16)
	VST2	[V2.B16, V3.B16], (R0)
encdone:
	RET

// V16..V19 hold the value table and V20 = '0'. VALUE replaces each character
// with its value plus 16, or with zero when it is not a hex digit. The marker
// bit falls out of SLI: it shifts past bit 7 on the high nibble and lies
// outside the four inserted-over bits on the low one.
#define VALUE(x) \
	VSUB	V20.B16, x, x \
	VTBL	x, [V16.B16, V17.B16, V18.B16, V19.B16], x

// func decodeNEON(dst, src []byte) int
TEXT ·decodeNEON(SB), NOSPLIT, $0-56
	MOVD	dst_len+8(FP), R1
	MOVD	src_len+32(FP), R3
	LSR	$4, R1, R1
	LSR	$5, R3, R3
	CMP	R1, R3
	CSEL	LT, R3, R1, R3
	MOVD	R3, R5
	CBZ	R3, decdone
	MOVD	dst_base+0(FP), R0
	MOVD	src_base+24(FP), R2
	MOVD	$hexTablesNEON<>(SB), R7
	ADD	$16, R7
	VLD1	(R7), [V16.B16, V17.B16, V18.B16, V19.B16]
	VMOVI	$48, V20.B16
	// LD2 deinterleaves the pair: V0 holds the high-nibble characters, V1
	// the low-nibble ones. Validate all 32 before the single store, so a
	// rejected block leaves the output and any in-place input untouched.
decode32:
	VLD2.P	32(R2), [V0.B16, V1.B16]
	VALUE(V0.B16)
	VALUE(V1.B16)
	VUMIN	V1.B16, V0.B16, V2.B16
	VUMINV	V2.B16, V2
	VMOV	V2.B[0], R6
	CBZ	R6, decdone
	VSLI	$4, V0.B16, V1.B16
	VST1.P	[V1.B16], 16(R0)
	SUBS	$1, R3
	BNE	decode32
decdone:
	SUB	R3, R5, R5
	LSL	$5, R5, R5
	MOVD	R5, ret+48(FP)
	RET
