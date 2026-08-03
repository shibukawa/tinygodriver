---
id: decision:jwt-package-reuse
type: decision
title: The JWT Package Moves In, But The Signer Is New
---
`popcornwave/contrib/jwt` is available to move into this repository. It supplies the token representation and the encoding discipline; it does not supply the operation api:google-auth actually needs, because it is built for the opposite direction.

```yaml
state: proposed
proposed_on: 2026-08-02
source: petitweb-go/contrib/jwt, module github.com/shibukawa/popcornwave
size: 517 lines of non-test Go across claims.go, jwks.go, sign.go, token.go, verify.go
tinygo: builds, verified 2026-08-02
it_is_already_jws:
  why_this_needs_saying: >
    nothing is named JWS in that package, so the move reads as if JWS still has
    to be written. It does not. A JWT is claims serialized as a JWS, so the
    three-segment form everyone calls a JWT is JWS Compact Serialization by
    definition, and this package implements it.
  evidence_in_the_code:
    Token.signingInput: the JWS Signing Input of RFC 7515 section 2, under that name
    sign.go: 'signingInput + "." + encode(signature), Compact Serialization, section 7.1'
    Header: "alg, typ, kid and crit, the JOSE header"
    verifySignature: the RFC 7518 algorithm dispatch
    hardening: "alg=none rejection and crit handling, both JWS-specific"
  scope: >
    the JWT subset of JWS only. Sign takes claims, not bytes, so JSON
    serialization, detached payloads, multiple signatures and non-JSON payloads
    are all absent. None of them is reachable from a self-signed JWT, so none is
    wanted; see decision:google-token-strategy.
what_is_actually_added:
  size_of_the_change: one algorithm, in one direction
  detail: >
    RS256 verification already exists, through rsa.VerifyPKCS1v15. Signing is
    blocked by a single guard, `signer.Algorithm() != "HS256"`, and the Signer
    interface is already the seam an RSASigner plugs into.
  not_added: a JWS layer, a second serialization, or any new token concept
already_present_for_google:
  kid: 'Header.KeyID, json:"kid,omitempty", which carries private_key_id'
  aud: >
    Claims.MarshalJSON emits a single-element Audience as a bare string rather
    than an array, which is the form decision:google-token-strategy needs
direction_mismatch:
  what_it_is: >
    a resource-server verifier. It parses and validates tokens other parties
    signed, with an algorithm allowlist, a JWKS resolver, and explicit rejection
    of alg=none, unknown critical headers, duplicate JSON members, non-canonical
    base64url and oversized input.
  what_this_needs: >
    a client-side minter. Build a claim set, sign it RS256 with a
    service-account key, send it. Nothing is ever verified.
  the_gap: >
    Sign() accepts HS256 only and rejects any other algorithm outright. RS256
    exists in that package for verification, through crypto/rsa VerifyPKCS1v15.
    Signing RS256 is not implemented, so the one function needed is the one
    missing.
what_transfers:
  - Header and Claims, including the Raw map for members outside the registered set
  - the JWS compact serialization in Sign, which is the reason to move a package rather than write one
  - the Signer interface, which is the seam an RS256 signer plugs into
  - the size bounds, worth keeping on a device that may parse a token it did not mint
what_does_not:
  jwks.go: >
    a key resolver for verifying third-party tokens. It also pulls math/big
    directly, to rebuild an RSA public key from JWKS n and e.
  verify.go: the whole verification half
  cost_if_kept: >
    dead code the linker can drop, so this is a maintenance question rather than
    a size one. It still argues for taking the package as a whole rather than
    forking a subset.
chosen:
  move: the package moves in as-is, keeping its API and its tests
  add: >
    an RSASigner implementing Signer, and a Sign path that accepts it. This is
    the change that has to go back upstream or be carried as a divergence, and
    it is worth agreeing before the move rather than after.
  signer_is_a_wrapper: >
    RSASigner delegates to api:rsa-signer rather than calling crypto/rsa, so the
    jwt package stays free of build tags and cgo. The Signer interface is
    exactly the seam that makes decision:native-rsa-signing invisible from here.
  where: >
    open. It is a general JWT package, not a Google one, so burying it in
    cloud/google would be wrong; a top-level jwt sibling of cloud/aws fits
    decision:package-layout better. Deferred until the second consumer exists.
  cloud_google_uses_it: >
    api:google-auth builds claims and calls Sign. The RSA key handling, PKCS#8
    parsing and token caching stay in cloud/google, since none of that is JWT.
naming:
  chosen: jwt, unchanged
  decided_on: 2026-08-02, delegated by the maintainer after the JWS question
  reason: >
    the package is offset from JWS in both directions, and jwt is the only name
    that misstates neither.
  more_than_jws: >
    validateClaims checks iss, aud, exp, nbf and iat. Those are RFC 7519 claim
    semantics, not JWS, and they are more than half the verification logic.
  less_than_jws: >
    Sign takes claims, not bytes. There is no entry point for an arbitrary
    payload, no JSON serialization, no detached payload and no multiple
    signatures.
  rejected:
    jws: >
      accurate about the serialization and wrong about the surface. Someone
      reaching for a package named jws wants to sign arbitrary bytes and would
      find they cannot. Subset-ness is the smaller error; promising generality
      that is absent is the larger one.
  the_confusion_was_documentation: >
    this concept described the transferable part as "base64url and
    segment-assembly discipline" instead of naming JWS. The code was never
    wrong, so renaming it would have been fixing the wrong artifact.
  churn: an existing package with existing callers in popcornwave, none of whom asked
  if_jws_is_ever_really_needed:
    trigger: a caller that must sign something which is not a claim set
    shape: >
      extract an internal jose core holding the signing input, the compact
      serialization and the algorithm dispatch, and leave jwt as the claims
      layer on top. Splitting is the honest move then; renaming is not.
    not_now: nothing in decision:google-token-strategy signs a non-claims payload
open_questions:
  - >
    whether Sign should stay algorithm-restricted at all once RS256 signing
    exists. The restriction is a security property for a verifier and an
    obstacle for a minter.
  - >
    which repository owns the package after the move, given it is currently
    under a different module
  - ES256, which neither package implements and requirement:google-auth-validation did not measure
consumes: requirement:google-auth-validation
consumed_by: api:google-auth
layout: decision:google-shared-package
