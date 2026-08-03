package rsasign

import (
	"bytes"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"os"
	"strings"
	"testing"
)

// These tests carry no build tag, so they run against crypto/rsa on a plain
// build and against the native backend under -tags force_tinygo_logic. Any
// disagreement between the two fails here rather than in production, which is
// the point: RSASSA-PKCS1-v1_5 is deterministic, so there is one right answer.

func readFile(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return b
}

func message(t *testing.T) []byte { return readFile(t, "message.txt") }

func wantSignature(t *testing.T, bits int) []byte {
	t.Helper()
	raw := strings.TrimSpace(string(readFile(t, sizeName(bits)+".sig.hex")))
	sig, err := hex.DecodeString(raw)
	if err != nil {
		t.Fatalf("decode expected signature: %v", err)
	}
	return sig
}

func sizeName(bits int) string {
	switch bits {
	case 2048:
		return "rsa2048"
	case 4096:
		return "rsa4096"
	}
	panic("no vector for that size")
}

func loadKey(t *testing.T, bits int) *Key {
	t.Helper()
	k, err := ParsePKCS8(readFile(t, sizeName(bits)+".pkcs8.pem"))
	if err != nil {
		t.Fatalf("ParsePKCS8: %v", err)
	}
	t.Cleanup(func() { _ = k.Close() })
	return k
}

// TestKnownAnswer is the contract every backend must meet. The expected value
// is committed, not recomputed here: recomputing it would make the test pass by
// construction.
func TestKnownAnswer(t *testing.T) {
	for _, bits := range []int{2048, 4096} {
		t.Run(sizeName(bits), func(t *testing.T) {
			key := loadKey(t, bits)
			if got := key.Bits(); got != bits {
				t.Errorf("Bits() = %d, want %d", got, bits)
			}
			digest := sha256.Sum256(message(t))
			got, err := key.SignPKCS1v15SHA256(digest[:])
			if err != nil {
				t.Fatalf("sign: %v", err)
			}
			want := wantSignature(t, bits)
			if !bytes.Equal(got, want) {
				t.Errorf("signature mismatch on backend %q\n got %d bytes %x\nwant %d bytes %x",
					Backend, len(got), got[:16], len(want), want[:16])
			}
		})
	}
}

// TestSignatureVerifies checks the signature against crypto/rsa rather than
// against the stored bytes, so a wrong-but-consistent vector cannot hide.
func TestSignatureVerifies(t *testing.T) {
	key := loadKey(t, 2048)
	digest := sha256.Sum256(message(t))
	sig, err := key.SignPKCS1v15SHA256(digest[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	blk, _ := pem.Decode(readFile(t, "rsa2048.pkcs8.pem"))
	parsed, err := x509.ParsePKCS8PrivateKey(blk.Bytes)
	if err != nil {
		t.Fatalf("parse for verification: %v", err)
	}
	pub := &parsed.(*rsa.PrivateKey).PublicKey
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], sig); err != nil {
		t.Errorf("backend %q produced a signature crypto/rsa rejects: %v", Backend, err)
	}
}

func TestSignIsDeterministic(t *testing.T) {
	key := loadKey(t, 2048)
	digest := sha256.Sum256(message(t))
	first, err := key.SignPKCS1v15SHA256(digest[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	second, err := key.SignPKCS1v15SHA256(digest[:])
	if err != nil {
		t.Fatalf("sign again: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Error("two signatures over the same digest differ; PKCS#1 v1.5 has no nonce")
	}
}

func TestUnwrapMatchesCommittedPKCS1(t *testing.T) {
	for _, bits := range []int{2048, 4096} {
		t.Run(sizeName(bits), func(t *testing.T) {
			blk, _ := pem.Decode(readFile(t, sizeName(bits)+".pkcs8.pem"))
			if blk == nil {
				t.Fatal("no PEM block")
			}
			got, err := pkcs1FromPKCS8(blk.Bytes)
			if err != nil {
				t.Fatalf("unwrap: %v", err)
			}
			if want := readFile(t, sizeName(bits)+".pkcs1.der"); !bytes.Equal(got, want) {
				t.Errorf("unwrapped %d bytes, want %d", len(got), len(want))
			}
		})
	}
}

func TestRejectsBadInput(t *testing.T) {
	valid, _ := pem.Decode(readFile(t, "rsa2048.pkcs8.pem"))

	truncated := append([]byte(nil), valid.Bytes[:len(valid.Bytes)/2]...)
	overlong := append(append([]byte(nil), valid.Bytes...), 0x00, 0x00)
	mistagged := append([]byte(nil), valid.Bytes...)
	mistagged[0] = 0x31 // SET instead of SEQUENCE

	cases := []struct {
		name string
		in   []byte
	}{
		{"empty", nil},
		{"one byte", []byte{0x30}},
		{"truncated", truncated},
		{"wrong outer tag", mistagged},
		{"not DER at all", []byte("this is not a key")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := pkcs1FromPKCS8(c.in); err == nil {
				t.Error("accepted input that is not a PKCS#8 RSA key")
			}
		})
	}

	// Trailing garbage after a well-formed PrivateKeyInfo is tolerated by the
	// walker, which reads the first element and ignores the rest. Asserted so
	// the behaviour is a decision rather than an accident.
	if _, err := pkcs1FromPKCS8(overlong); err != nil {
		t.Errorf("trailing bytes should be ignored, got %v", err)
	}
}

func TestParsePKCS8RejectsNonPEM(t *testing.T) {
	if _, err := ParsePKCS8([]byte("-----BEGIN NOTHING-----")); err == nil {
		t.Error("accepted a malformed PEM block")
	}
}

func TestDigestLengthIsChecked(t *testing.T) {
	key := loadKey(t, 2048)
	for _, n := range []int{0, 20, 31, 33, 64} {
		if _, err := key.SignPKCS1v15SHA256(make([]byte, n)); err != ErrBadDigest {
			t.Errorf("digest of %d bytes: err = %v, want ErrBadDigest", n, err)
		}
	}
}

func TestCloseIsIdempotentAndFinal(t *testing.T) {
	k, err := ParsePKCS8(readFile(t, "rsa2048.pkcs8.pem"))
	if err != nil {
		t.Fatalf("ParsePKCS8: %v", err)
	}
	if err := k.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := k.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
	digest := sha256.Sum256(message(t))
	if _, err := k.SignPKCS1v15SHA256(digest[:]); err != ErrClosed {
		t.Errorf("sign after Close: err = %v, want ErrClosed", err)
	}
}

func TestNilKeyDoesNotPanic(t *testing.T) {
	var k *Key
	if _, err := k.SignPKCS1v15SHA256(make([]byte, 32)); err != ErrClosed {
		t.Errorf("nil sign: err = %v, want ErrClosed", err)
	}
	if k.Bits() != 0 {
		t.Error("nil Bits() should be 0")
	}
	if err := k.Close(); err != nil {
		t.Errorf("nil Close: %v", err)
	}
}
