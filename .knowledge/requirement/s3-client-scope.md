---
id: requirement:s3-client-scope
type: requirement
title: S3 Client Scope
---
`storage/s3` covers whole-object operations with static or environment credentials, and nothing beyond that.

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
  credentials:
    - static values passed to WithCredentials
    - AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY, AWS_SESSION_TOKEN
  endpoints:
    - AWS regional endpoints, virtual-host addressed
    - S3-compatible servers, path addressed by default
out_of_scope:
  multipart_upload:
    reason: >
      the request shapes are verified to work under tinygo, but part tracking and
      abort handling are a second state machine the driver does not need yet
    consequence: Put sends one request, bounded by the endpoint limit, 5 GiB on AWS
  shared_credentials_file: no INI parser, so no ~/.aws/credentials or ~/.aws/config
  imds_and_sso: no metadata service, no container credentials, no SSO
  other_apis: no versioning, ACL, tagging, lifecycle, or presigning
payload_hashing:
  rule: sigv4 signs a hash of the body, so the body is read twice
  seekable: hashed in place and rewound
  otherwise: buffered in memory
  escape_hatch:
    option: WithUnsignedPayload
    caveat: >
      a stream with no known length goes out chunked, which AWS rejects for
      PutObject, so WithContentLength must accompany it
acceptance:
  - unit tests pass under both build configurations
  - integration tests pass against an S3-compatible endpoint, see rustfs in the README
  - a key containing a space, parentheses and multi-byte characters round-trips
decided_by: decision:no-aws-sdk-go-v2
