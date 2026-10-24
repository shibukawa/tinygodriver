// Package hmacsha256 computes HMAC-SHA256 digests without the hash objects
// crypto/hmac builds for each MAC. Those objects were five allocations on every
// SigV4 signature and every HS256 verification, which is a cost a TinyGo
// program pays in collector time on each request.
package hmacsha256

import "crypto/sha256"

// blockSize is SHA-256's block size, which is the width HMAC pads its key to.
const blockSize = 64

// Sum returns HMAC-SHA256 of data under key, as RFC 2104 defines it.
//
// Both passes are sha256.Sum256 over a buffer in this frame; the inner pass
// moves to the heap only when data is larger than the stack buffer, which a
// signing string or a token's signing input is not.
func Sum(key, data []byte) [sha256.Size]byte {
	var k [blockSize]byte
	if len(key) > blockSize {
		sum := sha256.Sum256(key)
		copy(k[:], sum[:])
	} else {
		copy(k[:], key)
	}

	var stack [blockSize + 512]byte
	inner := stack[:0]
	if blockSize+len(data) > len(stack) {
		inner = make([]byte, 0, blockSize+len(data))
	}
	for _, b := range k {
		inner = append(inner, b^0x36)
	}
	inner = append(inner, data...)
	innerSum := sha256.Sum256(inner)

	var outer [blockSize + sha256.Size]byte
	for i, b := range k {
		outer[i] = b ^ 0x5c
	}
	copy(outer[blockSize:], innerSum[:])
	return sha256.Sum256(outer[:])
}
