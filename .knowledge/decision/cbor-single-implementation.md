---
id: decision:cbor-single-implementation
type: decision
title: The Codec Moves, It Is Not Copied
---
There is exactly one implementation of this codec, in this repository. decision:jwt-package-reuse took the opposite trade for the signer, and its reasoning does not transfer.

```yaml
state: accepted 2026-08-19; this repository holds the implementation
state: proposed
proposed_on: 2026-08-19
chosen: popcornweb deletes its copy and consumes this one
the_jwt_precedent:
  what_it_did: copied the package and documented a deliberate divergence; both copies exist today
  why_that_holds: >
    a signer that diverges produces a token the other side rejects loudly, so
    the divergence announces itself on the first request
  why_it_fails_here: >
    a codec that diverges produces bytes both sides accept and read differently.
    That surfaces as a desync in a running game rather than as an error, and
    rule:cbor-determinism-contract is exactly the property a second copy erodes.
consequence: >
  any change to the encoding is a change to one file that both consumers rebuild
  against, and a version skew is a build fact rather than a runtime one
see: requirement:cbor-package-move
```
