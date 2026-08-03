---
id: rule:rsa-signer-agreement
type: rule
title: Every RSA Backend Produces The Same Bytes
---
All four api:rsa-signer backends must produce byte-identical signatures for the same key and message. Unlike rule:sigv4-wire-agreement, this is checkable offline and exactly.

```yaml
rule: >
  RSASSA-PKCS1-v1_5 is deterministic. There is no salt, no nonce and no
  timestamp, so two correct implementations cannot disagree. Any difference is
  a bug in one of them, not a variation.
consequence: >
  a known-answer test pins every backend against one fixed vector. No live
  peer, no network, no service account. This is the only native backend in this
  repository that can be fully verified without one.
contrast:
  sigv4: >
    rule:sigv4-wire-agreement takes its expected values from aws-sdk-go-v2 and
    still needs a real endpoint, because a signature is only wrong relative to
    what a server accepts
  tls: a handshake has no fixed answer to compare against at all
vector:
  source: a committed 2048-bit test key and a fixed message, with the expected signature
  generated_by: crypto/rsa on host go, which is the reference implementation
  checked_by: every backend, on every build path
  storage: a testdata file, not a generated fixture, so the expectation cannot drift
tests:
  - the same digest signs to the same 256 bytes on crypto/rsa and on the native backend
  - a signature from either backend verifies under crypto/rsa VerifyPKCS1v15
  - the DER walker turns the committed PKCS#8 key into the expected PKCS#1 bytes
  - a truncated, over-long, or wrongly tagged PKCS#8 input is rejected, not misread
  - Close twice is safe, and signing after Close returns an error rather than crashing
  - "a 4096-bit key round-trips, so the 512-byte output buffer is exercised"
do_not:
  - compare against a signature the service accepted, which proves less and costs a network
  - regenerate the expected value in the test, which would make it pass by construction
applies_to: api:rsa-signer
decided_by: decision:native-rsa-signing
precedent: rule:sigv4-wire-agreement
