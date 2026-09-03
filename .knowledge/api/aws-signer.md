---
id: api:aws-signer
type: api
title: AWS Signer And Credentials
---
`cloud/aws` holds what every AWS service client in this repository needs: SigV4 signing, credentials, environment resolution, and the HTTP client the build selects.

```yaml
import_path: github.com/shibukawa/tinygodriver/cloud/aws
state: shipped 2026-07-31
credentials: |
  type Credentials struct { AccessKeyID, SecretAccessKey, SessionToken string }
  func (c Credentials) Valid() bool
  func CredentialsFromEnv() Credentials   // AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY, AWS_SESSION_TOKEN
signing: |
  type SignRequest struct {
      Service          string     // "s3", "dynamodb"
      Region           string
      PayloadHash      string     // hex sha256, EmptyPayloadHash, or UnsignedPayload
      DoubleEncodePath bool       // every service except s3; see the rule
      Time             time.Time  // zero means now
  }
  func Sign(req *http.Request, creds Credentials, sr SignRequest)
  // Sign covers Request.Host when the caller set it, not URL.Host: the
  // signature covers the host header, and net/http sends the former.
  func Presign(req *http.Request, creds Credentials, sr SignRequest, expires time.Duration)
  // Presign writes the same signature into req.URL.RawQuery as X-Amz-*
  // parameters, sets no headers, and signs every header on req; see
  // rule:sigv4-service-parameterization for how the two header sets differ.
encoding: |
  func URIEncode(s string, encodeSlash bool) string
  func CanonicalQuery(params [][2]string) string
  func SHA256Hex(b []byte) string
constants:
  EmptyPayloadHash: sha256 of no bytes
  UnsignedPayload: "UNSIGNED-PAYLOAD", accepted by S3, not by DynamoDB
environment: |
  func RegionFromEnv() string                    // AWS_REGION, then AWS_DEFAULT_REGION
  func EndpointFromEnv(service string) string    // AWS_ENDPOINT_URL_<SERVICE>, then AWS_ENDPOINT_URL
transport: |
  const Backend string                           // "net/http" or "https"
  type ClientOptions struct { Timeout time.Duration; MaxIdleConnsPerHost int }
  func NewHTTPClient(opts ClientOptions) *http.Client
  func DisableRedirectFollowing(c *http.Client)
  func CloseIdleConnections(c *http.Client)      // works on either transport
pooling: >
  NewHTTPClient forwards MaxIdleConnsPerHost to https.Transport on the tinygo
  path and to net/http.Transport on the std path, so a service package tunes one
  field rather than two; see requirement:connection-reuse
errors:
  sentinels: ErrNoCredentials, ErrNoRegion
  reason: both services reject the same missing configuration before any request
non_goals:
  - no credential chain: no shared config file, no SSO, no IMDS, no os/exec providers
  - no retry policy, which is per-service; see requirement:dynamodb-retry-policy
  - no endpoint resolution rules engine; a service package builds its own URL
  - no request model or serialization, which stays in the service package
contract: rule:sigv4-service-parameterization
wire_rule: rule:sigv4-wire-agreement
build_tags: rule:build-tag-selection
decided_by: decision:aws-shared-package
consumers: api:s3-client, api:dynamodb-client
