---
id: requirement:google-auth-validation
type: requirement
title: Google Credential Math Validation Result
---
Measured state of Google service-account authentication under TinyGo. This is the evidence base for decision:google-token-strategy, and it is the one question a Datastore client has that a DynamoDB client does not.

```yaml
priority: must
question: >
  AWS signs with HMAC-SHA256, which tinygo has always had. Google signs a JWT
  assertion with RSA-SHA256 and reads a PKCS#8 PEM key to do it. If that does
  not work under tinygo, every credential path except a caller-supplied token
  disappears at once.
environment:
  date: 2026-08-02
  tinygo: 0.41.1 darwin/arm64, llvm 20.1.1
  go: 1.26.5
  method: >
    a standalone main package, built with both compilers, reading a
    service-account-shaped JSON file with a generated 2048-bit key
rs256:
  verdict: works, no gap against host go
  verified:
    - encoding/json decode of the service_account file shape
    - encoding/pem Decode of the PRIVATE KEY block
    - crypto/x509 ParsePKCS8PrivateKey to an "*rsa.PrivateKey", 2048 bit
    - crypto/rsa SignPKCS1v15 with crypto.SHA256 over the JWT signing input
    - VerifyPKCS1v15 against the derived public key
    - base64.RawURLEncoding for all three JWT segments
  cost:
    parse: 355us tinygo, 256us host go, once per process
    sign: 3.6-4.2ms tinygo, 1.1ms host go, steady state over five signatures
    reading: >
      tinygo is roughly 3.5x slower at RSA, which does not matter when a token
      is signed once an hour. It would matter if a signature were per-request,
      which is exactly what decision:google-token-strategy avoids.
where_the_slowdown_is:
  measured_on: 2026-08-02, same host, both compilers, us per operation
  table:
    sha256_600B: 0.356 host, 2.079 tinygo
    rsa2048_sign: 840 host, 2872 tinygo
    rsa2048_verify: 26.5 host, 98.7 tinygo
    rsa4096_sign: 5252 host, 19505 tinygo
    big_int_exp_2048: 1114 host, 5024 tinygo
  cause: >
    the multi-precision inner loop, not anything RSA-specific and not a missing
    CPU instruction: arm64 has no RSA instructions to miss. Go ships hand-written
    assembly for it in crypto/internal/fips140/bigmod, selected by the build tag
    "!purego && (386 || amd64 || arm || arm64 || ...)". Tinygo cannot assemble
    Plan 9 asm, so it sets purego, which selects nat_noasm.go, the portable Go
    path. Verified by compiling a file guarded on the purego tag under both
    compilers: host go false, tinygo true.
  evidence_it_is_the_loop: >
    the ratio is uniform, 3.4x to 4.5x, across raw big.Int.Exp and both RSA
    directions at both key sizes. A bare modexp at the same width shows the same
    gap as the signature that contains it, so nothing above the loop contributes.
  hashing_is_not_the_cost: >
    sha256 is 5.8x slower, the widest ratio here, because arm64 SHA-2 extensions
    are also reached through assembly. It is 0.356us against a 2872us signature,
    so it does not register.
  key_size_guidance: >
    2048 bit. A 4096-bit key costs 19.5ms per signature under tinygo, and modexp
    scales cubically, so the choice is worth making deliberately rather than
    inheriting from whatever the service account was created with.
  what_would_close_the_gap: >
    a native crypto backend, which was measured next and does close it
    completely; see native_rsa below and decision:native-rsa-signing
native_rsa:
  measured_on: 2026-08-02, darwin/arm64, Security.framework, hand-declared per rule:tinygo-darwin-toolchain
  verdict: works under tinygo, and it is the size that makes it worth doing
  verified:
    - SecKeyCreateWithData and SecKeyCreateSignature reached with no framework headers
    - "kSecKeyAlgorithmRSASignatureDigestPKCS1v15SHA256, so Go supplies the digest"
    - 256-byte signatures identical in length and accepted by the same verifier
    - Security and CoreFoundation link through the same literal SDK paths internal/securetransport uses
  size:
    pure_go: 1040 KB
    native: 583 KB
    baseline: 452 KB
    reading: 588 KB for crypto/rsa plus crypto/x509, against 131 KB for the bridge
  speed:
    native_tinygo: 1.10-1.17ms
    pure_go_tinygo: 2.87ms
    host_go: 1.05-1.16ms
    reading: >
      the purego penalty vanishes rather than shrinking, because the work moves
      into Apple's assembly
    key_load: 784us once per process
  pkcs8_unwrap:
    problem: SecKeyCreateWithData takes PKCS#1; Google ships PKCS#8
    result: >
      40 lines of hand-rolled DER, no encoding/asn1 and no crypto/x509. 291ns,
      1217 bytes in, 1191 out. This was the risk that decided whether the size
      argument held, and it held.
  not_measured:
    - the linux mbedtls path, though the vendored config already enables every needed module
    - the windows CNG path, blocked by requirement:windows-tinygo-feasibility
crypto_rand:
  verdict: >
    works; SignPKCS1v15 blinding draws from crypto/rand without a shim. Not
    needed on the native path, where the backend owns its own randomness.
not_yet_measured:
  - a real token exchange or self-signed JWT accepted by datastore.googleapis.com
  - the metadata-server credential path, which needs a GCE or Cloud Run host
  - ES256, used by some workload-identity flows
  - RSA under a non-darwin tinygo target, where the crypto backend differs
conclusion: >
  the credential math is not a blocker. Google auth is more machinery than
  SigV4, but the expensive part is a token round trip, not the signature.
reproduce: >
  tinygo build a program that reads a PKCS#8 PEM, signs a JWT header and claim
  set, and verifies its own signature
informs: decision:google-token-strategy, api:google-auth
precedent: requirement:dynamodb-driver-validation
