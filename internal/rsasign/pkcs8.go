package rsasign

import (
	"encoding/pem"
	"errors"
)

var (
	// ErrBadKey is returned for input that is not a usable PKCS#8 RSA key.
	ErrBadKey = errors.New("rsasign: not a PKCS#8 RSA private key")

	// ErrClosed is returned by a Key used after Close.
	ErrClosed = errors.New("rsasign: key is closed")

	// ErrBadDigest is returned for a digest that is not 32 bytes. Only SHA-256
	// is supported, so a wrong length is a caller mistake, not a variation.
	ErrBadDigest = errors.New("rsasign: digest must be 32 bytes")

	// ErrSignFailed is returned when a backend rejects an otherwise valid
	// request. It carries no detail because the native APIs report none worth
	// forwarding.
	ErrSignFailed = errors.New("rsasign: signing failed")

	// ErrPlatformNotSupported is returned on a build with no backend. It is
	// never a fallback to crypto/rsa: a silent fallback would satisfy the
	// no-crypto/rsa property on one build and break it on another.
	ErrPlatformNotSupported = errors.New("rsasign: no RSA backend for this platform")
)

// sha256Size is the only digest length this package accepts.
const sha256Size = 32

// ParsePKCS8 reads a PEM-wrapped PKCS#8 private key, the form a Google service
// account file carries, and prepares it for signing.
func ParsePKCS8(pemBytes []byte) (*Key, error) {
	blk, _ := pem.Decode(pemBytes)
	if blk == nil {
		return nil, ErrBadKey
	}
	return parsePKCS8DER(blk.Bytes)
}

// derElement reads one DER TLV from b, returning its tag, its contents, and
// whatever follows it.
//
// This is not a DER parser and must not become one. It reads exactly the shapes
// below and rejects everything else, which is what keeps it forty lines instead
// of linking crypto/x509. Its input is a key file the operator supplied, not a
// remote message.
func derElement(b []byte) (tag byte, body, rest []byte, err error) {
	if len(b) < 2 {
		return 0, nil, nil, ErrBadKey
	}
	tag = b[0]
	n := int(b[1])
	i := 2
	if n&0x80 != 0 {
		count := n & 0x7f
		// Indefinite length and lengths beyond four bytes are legal DER in
		// general and are not produced by any key file; refuse them rather
		// than grow a general decoder.
		if count == 0 || count > 4 || len(b) < 2+count {
			return 0, nil, nil, ErrBadKey
		}
		n = 0
		for j := 0; j < count; j++ {
			n = n<<8 | int(b[2+j])
		}
		i = 2 + count
	}
	if n < 0 || len(b) < i+n {
		return 0, nil, nil, ErrBadKey
	}
	return tag, b[i : i+n], b[i+n:], nil
}

const (
	tagInteger   = 0x02
	tagOctet     = 0x04
	tagSequence  = 0x30
	minKeyBits   = 2048
	maxKeyBits   = 8192
	maxSigLength = maxKeyBits / 8
)

// pkcs1FromPKCS8 returns the RSAPrivateKey inside a PrivateKeyInfo.
//
//	PrivateKeyInfo ::= SEQUENCE {
//	    version    INTEGER,
//	    algorithm  SEQUENCE,      -- AlgorithmIdentifier, not inspected
//	    privateKey OCTET STRING   -- RSAPrivateKey
//	}
//
// The algorithm OID is not checked here. A non-RSA key fails at rsaModulusBits
// below, or in the backend, and both report ErrBadKey; checking the OID as well
// would mean carrying an OID table for no additional rejection.
func pkcs1FromPKCS8(der []byte) ([]byte, error) {
	tag, body, _, err := derElement(der)
	if err != nil {
		return nil, err
	}
	if tag != tagSequence {
		return nil, ErrBadKey
	}
	tag, _, rest, err := derElement(body)
	if err != nil || tag != tagInteger {
		return nil, ErrBadKey
	}
	tag, _, rest, err = derElement(rest)
	if err != nil || tag != tagSequence {
		return nil, ErrBadKey
	}
	tag, key, _, err := derElement(rest)
	if err != nil || tag != tagOctet {
		return nil, ErrBadKey
	}
	return key, nil
}

// rsaModulusBits returns the bit length of the modulus of a PKCS#1
// RSAPrivateKey, which is both the key size and the signature length.
//
//	RSAPrivateKey ::= SEQUENCE { version INTEGER, modulus INTEGER, ... }
func rsaModulusBits(pkcs1 []byte) (int, error) {
	tag, body, _, err := derElement(pkcs1)
	if err != nil || tag != tagSequence {
		return 0, ErrBadKey
	}
	tag, _, rest, err := derElement(body)
	if err != nil || tag != tagInteger {
		return 0, ErrBadKey
	}
	tag, modulus, _, err := derElement(rest)
	if err != nil || tag != tagInteger {
		return 0, ErrBadKey
	}
	// DER signs its integers, so a modulus with the high bit set carries a
	// leading zero byte that is not part of the number.
	for len(modulus) > 0 && modulus[0] == 0 {
		modulus = modulus[1:]
	}
	if len(modulus) == 0 {
		return 0, ErrBadKey
	}
	bits := (len(modulus)-1)*8 + bitLen(modulus[0])
	if bits < minKeyBits || bits > maxKeyBits {
		return 0, ErrBadKey
	}
	return bits, nil
}

func bitLen(b byte) int {
	n := 0
	for b != 0 {
		n++
		b >>= 1
	}
	return n
}
