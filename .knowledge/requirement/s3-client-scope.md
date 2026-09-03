---
id: requirement:s3-client-scope
type: requirement
title: S3 Client Scope
---
`storage/s3` covers object operations, whole and in parts, with static or environment credentials, and nothing beyond that.

```yaml
priority: must
in_scope:
  operations:
    - GetObject, including Range
    - PutObject
    - HeadObject
    - DeleteObject
    - ListObjectsV2, one page at a time with a continuation token
    - CreateBucket, DeleteBucket
    - presigned URLs for GetObject, PutObject, HeadObject and DeleteObject
    - CreateMultipartUpload, UploadPart, CompleteMultipartUpload, AbortMultipartUpload
  credentials:
    - static values passed to WithCredentials
    - AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY, AWS_SESSION_TOKEN
  endpoints:
    - AWS regional endpoints, virtual-host addressed
    - S3-compatible servers, path addressed by default
out_of_scope:
  automatic_part_splitting:
    reason: the caller knows the part boundaries and the failure policy; the four calls are the primitives
    consequence: Put sends one request, bounded by the endpoint limit, 5 GiB on AWS
  list_parts_and_list_uploads: not asked for; an application tracks its own parts
  shared_credentials_file: no INI parser, so no ~/.aws/credentials or ~/.aws/config
  imds_and_sso: no metadata service, no container credentials, no SSO
  other_apis: no versioning, ACL, tagging, or lifecycle
payload_hashing:
  rule: sigv4 signs a hash of the body, so the body is read twice
  seekable: hashed in place and rewound
  otherwise: buffered in memory
  escape_hatch:
    option: WithUnsignedPayload
    caveat: >
      a stream with no known length goes out chunked, which AWS rejects for
      PutObject, so WithContentLength must accompany it
multipart:
  handle: MultipartUpload{Bucket, Key, UploadID} returned by Create and taken back by the other calls, so the three cannot drift apart
  parts: numbers 1..10000 checked locally as ErrInvalidPart; sizes enforced by the endpoint at completion
  metadata: Content-Type, Content-Encoding and x-amz-meta-* fixed at Create through the Put options
  body: UploadPart follows Put's hashing and buffering rules; WithContentLength is the one Put option that applies
  complete_200_error: a 200 carrying an Error document is reported as an error, as a 4xx is
  abort: NoSuchUpload is ErrNoSuchUpload; abort on every failure path, since AWS bills orphaned parts
  browser_part: Presign with uploadId and partNumber in PresignOptions.Query
  added_by: decision:s3-presign-in-scope
presigning:
  form: sigv4 query-string signing, X-Amz-* parameters plus X-Amz-Signature last
  payload: UNSIGNED-PAYLOAD; the sender never sees the credentials, the signer never sees the body
  signed_headers: host plus what PresignOptions names; each one is a constraint the sender must reproduce
  response_shaping: response-content-* parameters through PresignOptions.Query, never a signed header, because a link cannot add one
  expiry: default 15 min, cap 7 days enforced as ErrPresignExpiry, seconds rounded up
  clock: Client.now, so a pinned test fixes the signature
  no_network: pure function of the client and the arguments; ctx is unused
  known_answer: the S3 documentation's presigned GET example, see rule:sigv4-service-parameterization
  added_by: decision:s3-presign-in-scope
acceptance:
  - unit tests pass under both build configurations
  - integration tests pass against an S3-compatible endpoint, see rustfs in the README
  - a key containing a space, parentheses and multi-byte characters round-trips
  - >
    a presigned PUT and GET round-trip through a plain http.Client against
    rustfs; a PUT with another Content-Type and a URL with an edited expiry are
    refused with 403
  - >
    two 5 MiB parts through the client and a third through a presigned part
    URL assemble against rustfs and read back byte for byte with the metadata
    fixed at Create; a second abort is ErrNoSuchUpload
decided_by: decision:no-aws-sdk-go-v2
