package hmacsha256

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"math/rand"
	"testing"
)

// Sum is checked against crypto/hmac across the key and data sizes that take
// different paths: keys shorter than, at, and longer than the block, and data
// that fits the stack buffer or does not.
func TestSumMatchesCryptoHMAC(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for _, keyLen := range []int{0, 1, 16, 32, 63, 64, 65, 100, 200} {
		for _, dataLen := range []int{0, 1, 55, 56, 64, 100, 447, 448, 449, 512, 513, 1000, 4096} {
			key := make([]byte, keyLen)
			data := make([]byte, dataLen)
			rng.Read(key)
			rng.Read(data)
			got := Sum(key, data)
			mac := hmac.New(sha256.New, key)
			mac.Write(data)
			if want := mac.Sum(nil); !bytes.Equal(got[:], want) {
				t.Fatalf("Sum(key %d bytes, data %d bytes) = %x, want %x", keyLen, dataLen, got, want)
			}
		}
	}
}

func TestSumDoesNotAllocateForShortData(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	data := bytes.Repeat([]byte("x"), 300)
	var sink [sha256.Size]byte
	if allocs := testing.AllocsPerRun(100, func() { sink = Sum(key, data) }); allocs != 0 {
		t.Fatalf("Sum allocated %.0f times for %d bytes of data", allocs, len(data))
	}
	_ = sink
}
