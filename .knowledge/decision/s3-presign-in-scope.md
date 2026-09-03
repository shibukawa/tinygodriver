---
id: decision:s3-presign-in-scope
type: decision
title: Presigned URLs And Multipart Are In The S3 Client's Scope
---
`storage/s3` gains `Presign` and the four multipart calls; both leave the exclusions of requirement:s3-client-scope, whose `presigning` and `multipart` blocks hold the contracts.

```yaml
state: accepted
accepted_on: 2026-09-03
requested_by: github.com/shibukawa/popcornweb, whose storage.Bucket wraps api:s3-client as its process-host backend; change request against v1.2.11
why_here_and_not_downstream:
  - presigning is a second output of api:aws-signer, not a bucket-management API like versioning, ACL, tagging or lifecycle
  - a downstream copy of the canonical request over the exported helpers drifts by one encoding rule, and that is a SignatureDoesNotMatch no downstream test catches
  - the Cloudflare R2 binding has no presign; R2's presigned URL is a SigV4 query-signed URL against its S3 endpoint, so this signer serves every host the downstream deploys to
choices:
  placement: on *Client beside Get and Put, so endpoint, region, addressing and credentials are never repeated by the caller
  signer_shares_builder: aws.Presign and aws.Sign use one canonical request builder; the refactor kept BenchmarkSign at 18 allocs
  sign_every_header: >
    the query form signs every header on the request, not the Sign whitelist,
    see rule:sigv4-service-parameterization presign_form; the caller named each
    one so the sender would have to reproduce it
  query_field: >
    PresignOptions.Query was added beyond the requested shape. The downstream's
    Content-Disposition-on-GET example cannot be a signed request header, since
    a link sends none; S3 takes it as response-content-disposition in the query.
    Query also carries uploadId and partNumber, the only interaction with a
    future multipart upload
  default_expiry: 15 minutes, matching the AWS SDKs; the 7-day cap is enforced rather than documented
multipart_upload:
  state: in scope as of 2026-09-03, on the repository owner's call
  downstream_position: filed at low priority with no application above the single-Put limit; nothing there waits on it
  shape: the four operations as methods, the part list as []CompletedPart{PartNumber, ETag}, as the request proposed
  handle_type: >
    MultipartUpload is passed by value to the three later calls rather than
    bucket, key and upload ID separately, so a mismatched triple cannot be
    expressed; the request left this open
  no_splitting: no helper that slices a stream into parts; the caller owns part boundaries and the abort policy
  presign_interaction: a part upload is Presign with uploadId and partNumber in Query, which the query_field choice already covers
```
