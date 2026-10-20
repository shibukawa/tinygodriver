//go:build arm64 && !tinygo && !base64_c && !purego

#include "textflag.h"

// Go-assembler port of vector_tinygo.c's NEON kernel. Every load and store is
// bounded by the remaining slice lengths, and decoded output never overtakes
// unread input, so decoding in place is safe. Neither function uses a stack
// frame or calls another function.

// Decode tables: per alphabet hi, lo, delta (standard at 0, URL at 48), then
// tail4, tail8, tail12, the single-vector pack selectors and their shifts.
// The classifier tables are adapted from simdutf; see NOTICE.
DATA decodeTablesNEON<>+0(SB)/8, $0x0808060000000000
DATA decodeTablesNEON<>+8(SB)/8, $0x4080804010202010
DATA decodeTablesNEON<>+16(SB)/8, $0xf8f8f8f8f8f8f8a9
DATA decodeTablesNEON<>+24(SB)/8, $0xa4a0a1a1a2f1f9f8
DATA decodeTablesNEON<>+32(SB)/8, $0x00131004bfbfb9b9
DATA decodeTablesNEON<>+40(SB)/8, $0x0000000000000000
DATA decodeTablesNEON<>+48(SB)/8, $0x0808040000000000
DATA decodeTablesNEON<>+56(SB)/8, $0x4080804012202010
DATA decodeTablesNEON<>+64(SB)/8, $0xf8f8f8f8f8f8f8a9
DATA decodeTablesNEON<>+72(SB)/8, $0xa2a0a5a1a0f1f9f8
DATA decodeTablesNEON<>+80(SB)/8, $0x00e01104bfbfb9b9
DATA decodeTablesNEON<>+88(SB)/8, $0x0000000000000000
DATA decodeTablesNEON<>+96(SB)/8, $0x0000000000000000
DATA decodeTablesNEON<>+104(SB)/8, $0x0101010100000000
DATA decodeTablesNEON<>+112(SB)/8, $0x0000000000000000
DATA decodeTablesNEON<>+120(SB)/8, $0x0101010101010101
DATA decodeTablesNEON<>+128(SB)/8, $0x0101010100000000
DATA decodeTablesNEON<>+136(SB)/8, $0x0101010101010101
DATA decodeTablesNEON<>+144(SB)/8, $0x0908060504020100
DATA decodeTablesNEON<>+152(SB)/8, $0xffffffff0e0d0c0a
DATA decodeTablesNEON<>+160(SB)/8, $0x0a09070605030201
DATA decodeTablesNEON<>+168(SB)/8, $0xffffffff0f0e0d0b
DATA decodeTablesNEON<>+176(SB)/8, $0x0402060402060402
DATA decodeTablesNEON<>+184(SB)/8, $0x0000000006040206
DATA decodeTablesNEON<>+192(SB)/8, $0xfefc00fefc00fefc
DATA decodeTablesNEON<>+200(SB)/8, $0x0000000000fefc00
GLOBL decodeTablesNEON<>(SB), RODATA|NOPTR, $208

// Encode tables: the standard alphabet, the URL alphabet's last 16 entries at
// 64, then the single-vector pack selectors and shifts at 80.
DATA encodeTablesNEON<>+0(SB)/8, $0x4847464544434241
DATA encodeTablesNEON<>+8(SB)/8, $0x504f4e4d4c4b4a49
DATA encodeTablesNEON<>+16(SB)/8, $0x5857565554535251
DATA encodeTablesNEON<>+24(SB)/8, $0x6665646362615a59
DATA encodeTablesNEON<>+32(SB)/8, $0x6e6d6c6b6a696867
DATA encodeTablesNEON<>+40(SB)/8, $0x767574737271706f
DATA encodeTablesNEON<>+48(SB)/8, $0x333231307a797877
DATA encodeTablesNEON<>+56(SB)/8, $0x2f2b393837363534
DATA encodeTablesNEON<>+64(SB)/8, $0x333231307a797877
DATA encodeTablesNEON<>+72(SB)/8, $0x5f2d393837363534
DATA encodeTablesNEON<>+80(SB)/8, $0x0504030302010000
DATA encodeTablesNEON<>+88(SB)/8, $0x0b0a090908070606
DATA encodeTablesNEON<>+96(SB)/8, $0xff0504ffff0201ff
DATA encodeTablesNEON<>+104(SB)/8, $0xff0b0affff0807ff
DATA encodeTablesNEON<>+112(SB)/8, $0x000204fe000204fe
DATA encodeTablesNEON<>+120(SB)/8, $0x000204fe000204fe
DATA encodeTablesNEON<>+128(SB)/8, $0x00fafc0000fafc00
DATA encodeTablesNEON<>+136(SB)/8, $0x00fafc0000fafc00
GLOBL encodeTablesNEON<>(SB), RODATA|NOPTR, $144

// V16 hi, V17 lo, V18 delta, V19 = 15. CLASSIFY sets v to a single class
// bit for every valid character and zero otherwise.
#define CLASSIFY(x, v, t) \
	VAND	V19.B16, x, t \
	VTBL	t, [V17.B16], t \
	VUSHR	$3, x, v \
	VTBL	v, [V16.B16], v \
	VAND	t, v, v

// The class bit's leading-zero count selects the decoding offset.
#define TRANSLATE(x, v) \
	VCLZ	v, v \
	VTBL	v, [V18.B16], v \
	VADD	v, x, x

// Four deinterleaved sextet vectors (V0..V3) become three byte vectors
// (V10..V12) for ST3.
#define PACK3 \
	VUSHR	$4, V1.B16, V10.B16 \
	VSLI	$2, V0.B16, V10.B16 \
	VUSHR	$2, V2.B16, V11.B16 \
	VSLI	$4, V1.B16, V11.B16 \
	VORR	V3.B16, V3.B16, V12.B16 \
	VSLI	$6, V2.B16, V12.B16

// Pack one sequential 16-sextet vector into its first 12 bytes.
#define PACK1(x, out, t) \
	VTBL	V23.B16, [x], out \
	VUSHL	V25.B16, out, out \
	VTBL	V24.B16, [x], t \
	VUSHL	V26.B16, t, t \
	VORR	t, out, out

// func decodeNEON(dst, src []byte, url bool) (read, written int)
TEXT ·decodeNEON(SB), NOSPLIT, $0-72
	MOVD	dst_base+0(FP), R0
	MOVD	dst_len+8(FP), R1
	MOVD	src_base+24(FP), R2
	MOVD	src_len+32(FP), R3
	MOVBU	url+48(FP), R6
	MOVD	R2, R4
	MOVD	R0, R5
	MOVD	$decodeTablesNEON<>(SB), R7
	CBZ	R6, std
	ADD	$48, R7, R8
	VLD1	(R8), [V16.B16, V17.B16, V18.B16]
	B	tables
std:
	VLD1	(R7), [V16.B16, V17.B16, V18.B16]
tables:
	ADD	$96, R7, R8
	VLD1.P	64(R8), [V20.B16, V21.B16, V22.B16, V23.B16]
	VLD1	(R8), [V24.B16, V25.B16, V26.B16]
	VMOVI	$15, V19.B16
	CMP	$256, R3
	BLT	small

lead:
	CBZ	R3, mime
	MOVBU	(R2), R6
	CMP	$10, R6
	BEQ	leadskip
	CMP	$13, R6
	BNE	mime
leadskip:
	ADD	$1, R2
	SUB	$1, R3
	B	lead

	// MIME lines: 76 digits, then LF or CR/LF. The fifth vector reads four
	// separator/next-line bytes; tail4 excludes them from validation.
mime:
	CMP	$80, R3
	BLT	plain
	CMP	$57, R1
	BLT	plain
	MOVBU	76(R2), R6
	CMP	$10, R6
	BEQ	mimeblock
	CMP	$13, R6
	BNE	plain
mimeblock:
	ADD	$64, R2, R8
	VLD4	(R2), [V0.B16, V1.B16, V2.B16, V3.B16]
	VLD1	(R8), [V4.B16]
	CLASSIFY(V0.B16, V5.B16, V10.B16)
	CLASSIFY(V1.B16, V6.B16, V11.B16)
	CLASSIFY(V2.B16, V7.B16, V12.B16)
	CLASSIFY(V3.B16, V8.B16, V13.B16)
	CLASSIFY(V4.B16, V9.B16, V14.B16)
	VORR	V20.B16, V9.B16, V14.B16
	VUMIN	V5.B16, V6.B16, V10.B16
	VUMIN	V7.B16, V8.B16, V11.B16
	VUMIN	V10.B16, V11.B16, V10.B16
	VUMIN	V14.B16, V10.B16, V10.B16
	VUMINV	V10.B16, V10
	VMOV	V10.B[0], R6
	CBZ	R6, plain
	TRANSLATE(V0.B16, V5.B16)
	TRANSLATE(V1.B16, V6.B16)
	TRANSLATE(V2.B16, V7.B16)
	TRANSLATE(V3.B16, V8.B16)
	TRANSLATE(V4.B16, V9.B16)
	PACK3
	PACK1(V4.B16, V13.B16, V14.B16)
	VST3	[V10.B16, V11.B16, V12.B16], (R0)
	VMOV	V13.D[0], R8
	MOVD	R8, 48(R0)
	VMOV	V13.B[8], R8
	MOVB	R8, 56(R0)
	MOVBU	76(R2), R6
	ADD	$77, R2
	SUB	$77, R3
	ADD	$57, R0
	SUB	$57, R1
	CMP	$13, R6
	BNE	mime
	MOVBU	(R2), R6
	CMP	$10, R6
	BNE	mime
	ADD	$1, R2
	SUB	$1, R3
	B	mime

	// Four vectors share one validity decision. A rejected block leaves
	// output untouched and continues in the single-vector loop.
plain:
	CMP	$64, R3
	BLT	small
	CMP	$48, R1
	BLT	small
	MOVBU	(R2), R6
	CMP	$10, R6
	BEQ	plainskip
	CMP	$13, R6
	BEQ	plainskip
	VLD4	(R2), [V0.B16, V1.B16, V2.B16, V3.B16]
	CLASSIFY(V0.B16, V5.B16, V10.B16)
	CLASSIFY(V1.B16, V6.B16, V11.B16)
	CLASSIFY(V2.B16, V7.B16, V12.B16)
	CLASSIFY(V3.B16, V8.B16, V13.B16)
	VUMIN	V5.B16, V6.B16, V10.B16
	VUMIN	V7.B16, V8.B16, V11.B16
	VUMIN	V10.B16, V11.B16, V10.B16
	VUMINV	V10.B16, V10
	VMOV	V10.B[0], R6
	CBZ	R6, small
	TRANSLATE(V0.B16, V5.B16)
	TRANSLATE(V1.B16, V6.B16)
	TRANSLATE(V2.B16, V7.B16)
	TRANSLATE(V3.B16, V8.B16)
	PACK3
	VST3.P	[V10.B16, V11.B16, V12.B16], 48(R0)
	ADD	$64, R2
	SUB	$64, R3
	SUB	$48, R1
	B	plain
plainskip:
	ADD	$1, R2
	SUB	$1, R3
	B	plain

	// Single vectors, CR/LF between quanta and 12/8/4-digit fringes.
small:
	CMP	$16, R3
	BLT	smalltail
	CMP	$16, R1
	BLT	done
	VLD1	(R2), [V0.B16]
	CLASSIFY(V0.B16, V5.B16, V10.B16)
	VUMINV	V5.B16, V7
	VMOV	V7.B[0], R6
	VORR	V5.B16, V5.B16, V6.B16
	TRANSLATE(V0.B16, V6.B16)
	PACK1(V0.B16, V2.B16, V10.B16)
	CBZ	R6, fringe
	VST1	[V2.B16], (R0)
	ADD	$16, R2
	SUB	$16, R3
	ADD	$12, R0
	SUB	$12, R1
	B	small
smallskip:
	ADD	$1, R2
	SUB	$1, R3
	B	small

	// Fewer than 16 bytes remain; skip separators before returning so the
	// scalar suffix starts at a digit, as after a rejected vector.
smalltail:
	CBZ	R3, done
	MOVBU	(R2), R6
	CMP	$10, R6
	BEQ	smallskip
	CMP	$13, R6
	BEQ	smallskip
	B	done

	// Check separators only after a vector fails validation.
fringe:
	MOVBU	(R2), R7
	CMP	$10, R7
	BEQ	smallskip
	CMP	$13, R7
	BEQ	smallskip
	VORR	V20.B16, V5.B16, V7.B16
	VUMINV	V7.B16, V7
	VMOV	V7.B[0], R6
	CBNZ	R6, fringe12
	VORR	V21.B16, V5.B16, V7.B16
	VUMINV	V7.B16, V7
	VMOV	V7.B[0], R6
	CBNZ	R6, fringe8
	VORR	V22.B16, V5.B16, V7.B16
	VUMINV	V7.B16, V7
	VMOV	V7.B[0], R6
	CBZ	R6, done
	VMOV	V2.H[0], R8
	MOVH	R8, (R0)
	VMOV	V2.B[2], R8
	MOVB	R8, 2(R0)
	ADD	$4, R2
	SUB	$4, R3
	ADD	$3, R0
	SUB	$3, R1
	B	small
fringe8:
	VMOV	V2.S[0], R8
	MOVW	R8, (R0)
	VMOV	V2.H[2], R8
	MOVH	R8, 4(R0)
	ADD	$8, R2
	SUB	$8, R3
	ADD	$6, R0
	SUB	$6, R1
	B	small
fringe12:
	VMOV	V2.D[0], R8
	MOVD	R8, (R0)
	VMOV	V2.B[8], R8
	MOVB	R8, 8(R0)
	ADD	$12, R2
	SUB	$12, R3
	ADD	$9, R0
	SUB	$9, R1
	B	small

done:
	SUB	R4, R2, R2
	SUB	R5, R0, R0
	MOVD	R2, read+56(FP)
	MOVD	R0, written+64(FP)
	RET

// V16..V19 alphabet, V20 = 63, V21..V24 single-vector selectors/shifts. Split three bytes into four sextets.
#define SEXTETS(arr) \
	VUSHR	$2, V0.arr, V4.arr \
	VUSHR	$4, V1.arr, V5.arr \
	VSLI	$4, V0.arr, V5.arr \
	VAND	V20.arr, V5.arr, V5.arr \
	VUSHR	$6, V2.arr, V6.arr \
	VSLI	$2, V1.arr, V6.arr \
	VAND	V20.arr, V6.arr, V6.arr \
	VAND	V20.arr, V2.arr, V7.arr \
	VTBL	V4.arr, [V16.B16, V17.B16, V18.B16, V19.B16], V4.arr \
	VTBL	V5.arr, [V16.B16, V17.B16, V18.B16, V19.B16], V5.arr \
	VTBL	V6.arr, [V16.B16, V17.B16, V18.B16, V19.B16], V6.arr \
	VTBL	V7.arr, [V16.B16, V17.B16, V18.B16, V19.B16], V7.arr

// func encodeNEON(dst, src []byte, url bool) int
TEXT ·encodeNEON(SB), NOSPLIT, $0-64
	MOVD	dst_base+0(FP), R0
	MOVD	dst_len+8(FP), R1
	MOVD	src_base+24(FP), R2
	MOVD	src_len+32(FP), R3
	MOVBU	url+48(FP), R6
	MOVD	R2, R4
	MOVD	$encodeTablesNEON<>(SB), R7
	VLD1	(R7), [V16.B16, V17.B16, V18.B16, V19.B16]
	CBZ	R6, encstd
	ADD	$64, R7, R8
	VLD1	(R8), [V19.B16]
encstd:
	ADD	$80, R7, R8
	VLD1	(R8), [V21.B16, V22.B16, V23.B16, V24.B16]
	VMOVI	$63, V20.B16
encode48:
	CMP	$48, R3
	BLT	encode24
	CMP	$64, R1
	BLT	encode24
	VLD3.P	48(R2), [V0.B16, V1.B16, V2.B16]
	SEXTETS(B16)
	VST4.P	[V4.B16, V5.B16, V6.B16, V7.B16], 64(R0)
	SUB	$48, R3
	SUB	$64, R1
	B	encode48
encode24:
	CMP	$24, R3
	BLT	encode12
	CMP	$32, R1
	BLT	encode12
	VLD3.P	24(R2), [V0.B8, V1.B8, V2.B8]
	SEXTETS(B8)
	VST4.P	[V4.B8, V5.B8, V6.B8, V7.B8], 32(R0)
	SUB	$24, R3
	SUB	$32, R1
	B	encode24

	// Load 16 bytes and consume 12, leaving at most 15 bytes to scalar code.
encode12:
	CMP	$16, R3
	BLT	encdone
	CMP	$16, R1
	BLT	encdone
	VLD1	(R2), [V0.B16]
	VTBL	V21.B16, [V0.B16], V4.B16
	VUSHL	V23.B16, V4.B16, V4.B16
	VTBL	V22.B16, [V0.B16], V5.B16
	VUSHL	V24.B16, V5.B16, V5.B16
	VORR	V5.B16, V4.B16, V4.B16
	VAND	V20.B16, V4.B16, V4.B16
	VTBL	V4.B16, [V16.B16, V17.B16, V18.B16, V19.B16], V4.B16
	VST1.P	[V4.B16], 16(R0)
	ADD	$12, R2
	SUB	$12, R3
	SUB	$16, R1
	B	encode12
encdone:
	SUB	R4, R2, R2
	MOVD	R2, ret+56(FP)
	RET
