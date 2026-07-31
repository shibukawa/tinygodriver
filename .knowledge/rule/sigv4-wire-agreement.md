---
id: rule:sigv4-wire-agreement
type: rule
title: The Signature Covers What Goes On The Wire
---
The canonical request is built from the encoded path and query already stored on the `*url.URL`, never from a separately encoded copy.

```yaml
problem: >
  sigv4 for S3 hashes the request line as sent. Go escapes a path by its own
  rules, which leave sub-delimiters such as "(" and ")" literal, while the AWS
  encoding escapes everything outside the unreserved set. Encoding the path twice,
  once for the signature and once by net/http, produces two different strings and
  a SignatureDoesNotMatch that only shows up on AWS.
mechanism:
  build: |
    u.Path    = decoded path
    u.RawPath = uriEncode(path, false)
    u.RawQuery = canonicalQuery(params)
  sign: |
    canonical URI   = req.URL.EscapedPath()   // returns RawPath
    canonical query = req.URL.RawQuery
  send: net/http writes URL.RequestURI(), which is the same EscapedPath
rules:
  - never pass a pre-escaped string to http.NewRequest and re-escape it for signing
  - after a redirect, re-apply uriEncode to the new Location path
  - canonicalQuery keeps "=" for an empty value, which subresources such as ?uploads need
  - a header is signed only if it is on the request; Content-Length is not, because
    net/http writes it from Request.ContentLength rather than the header map
tested_by:
  - TestEscapedPathMatchesSignature asserts EscapedPath equals RequestURI equals the AWS form
  - TestPutSignsAndSendsBody asserts the request line the server receives
  - the integration test uses a key with a space, parentheses and multi-byte characters
scope_note: >
  this is the S3 canonicalization rule. It is single-encoding, which the SigV4
  spec reserves for S3; other services normalize and double-encode the path. The
  shared signer takes that choice as an argument, see
  rule:sigv4-service-parameterization.
applies_to: storage/s3 request building, cloud/aws encoding helpers
