// Package rsasign computes RSASSA-PKCS1-v1_5 signatures over a SHA-256 digest,
// through crypto/rsa on host Go and through the OS on TinyGo builds.
//
// It exists for binary size. Linking crypto/rsa and crypto/x509 into a TinyGo
// build costs about 588 KB, on a target where the whole HTTPS client is 272 KB
// on darwin; reaching the same operation through Security.framework costs about
// 131 KB. Speed is a side effect and not the point: TinyGo compiles the
// multi-precision inner loop from portable Go rather than assembly, because it
// sets the purego build tag, so a pure-Go signature takes 2.9 ms against
// 1.1 ms natively. At one signature per token lifetime neither number matters.
//
// The only caller is cloud/google, which signs one JWT per hour. The surface is
// deliberately narrow: one algorithm, one direction, no verification. Verifying
// happens in tests, on host Go, where crypto/rsa is free.
//
//	key, err := rsasign.ParsePKCS8(pemBytes)
//	defer key.Close()
//	digest := sha256.Sum256(signingInput)
//	sig, err := key.SignPKCS1v15SHA256(digest[:])
//
// Close releases the native handle. A Key that is dropped without it leaks one,
// the same obligation the TLS backends carry.
//
// Because RSASSA-PKCS1-v1_5 is deterministic — no salt, no nonce — every
// backend must produce byte-identical signatures for the same key and message.
// That is what testdata pins, and it is the reason this is the one native
// backend in this repository verifiable without a live peer.
package rsasign
