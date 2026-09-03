---
id: api:s3-client
type: api
title: S3 Client
---
`Client` issues signed S3 REST requests over whichever HTTP stack the build selects, and decodes the XML replies.

```yaml
constructor: func New(opts ...Option) (*Client, error)
methods:
  - func (c *Client) Get(ctx context.Context, bucket, key string) (*Object, error)
  - func (c *Client) GetRange(ctx context.Context, bucket, key string, offset, length int64) (*Object, error)
  - func (c *Client) Put(ctx context.Context, bucket, key string, body io.Reader, opts ...PutOption) (*PutResult, error)
  - func (c *Client) Head(ctx context.Context, bucket, key string) (*ObjectInfo, error)
  - func (c *Client) Delete(ctx context.Context, bucket, key string) error
  - func (c *Client) List(ctx context.Context, bucket string, opts ...ListOption) (*ListResult, error)
  - func (c *Client) CreateBucket(ctx context.Context, bucket string) error
  - func (c *Client) DeleteBucket(ctx context.Context, bucket string) error
  - func (c *Client) Presign(ctx context.Context, bucket, key string, opts PresignOptions) (*url.URL, error)
  - func (c *Client) CreateMultipartUpload(ctx context.Context, bucket, key string, opts ...PutOption) (*MultipartUpload, error)
  - func (c *Client) UploadPart(ctx context.Context, upload MultipartUpload, partNumber int, body io.Reader, opts ...PutOption) (*CompletedPart, error)
  - func (c *Client) CompleteMultipartUpload(ctx context.Context, upload MultipartUpload, parts []CompletedPart) (*PutResult, error)
  - func (c *Client) AbortMultipartUpload(ctx context.Context, upload MultipartUpload) error
  - func (c *Client) Region() string
  - func (c *Client) Endpoint() string
options:
  client: WithEndpoint, WithRegion, WithCredentials, WithCredentialsFromEnv, WithPathStyle, WithUnsignedPayload, WithTimeout, WithHTTPClient
  put: WithContentType, WithContentEncoding, WithContentLength, WithMetadata
  list: WithPrefix, WithDelimiter, WithMaxKeys, WithStartAfter, WithContinuationToken
  multipart: |
    type MultipartUpload struct { Bucket, Key, UploadID string }
    type CompletedPart struct { PartNumber int; ETag string }
    const MinPartNumber, MaxPartNumber = 1, 10000
  presign: |
    type PresignOptions struct {
        Method      string            // GET, PUT, HEAD, DELETE; empty is GET
        Expires     time.Duration     // zero is DefaultPresignExpiry, above MaxPresignExpiry is ErrPresignExpiry
        ContentType string            // signed when set
        Headers     map[string]string // signed; the sender must reproduce each
        Query       map[string]string // signed with the URL; response-content-*, uploadId, partNumber
    }
defaults:
  credentials: CredentialsFromEnv when no credentials option is given
  region: AWS_REGION, then AWS_DEFAULT_REGION; absent is ErrNoRegion
  endpoint: AWS_ENDPOINT_URL_S3, then AWS_ENDPOINT_URL, then https://s3.<region>.amazonaws.com
  addressing: virtual host for amazonaws.com, path style elsewhere
  timeout: 60s
  presign_expiry: DefaultPresignExpiry 15m, MaxPresignExpiry 7d
implementation:
  shared: signing, request building, XML decoding; the multipart replies go through parseFlatDocument, a visitor over a root of text elements
  signing_and_credentials: >
    api:aws-signer. s3.Credentials is an alias and s3.CredentialsFromEnv a
    wrapper, so this API is unchanged by decision:aws-shared-package.
  std_go: net/http with crypto/tls, Backend == "net/http"
  tinygo: api:https-transport, Backend == "https"
  redirects: requirement:s3-redirect-resigning
errors:
  type: "*s3.Error with Op, Bucket, Key, StatusCode, Code, Message, RequestID"
  sentinels: ErrNoSuchKey, ErrNoSuchBucket, ErrAccessDenied, ErrBucketExists, ErrBucketNotEmpty, ErrInvalidRange, ErrBadCredentials, ErrNoCredentials, ErrNoRegion, ErrTooManyRedirect, ErrPresignExpiry, ErrNoSuchUpload, ErrInvalidPart
  mapping: the Code element first, then the status code when there is no error document
  invalid_part: InvalidPart, InvalidPartOrder and EntityTooSmall all map to ErrInvalidPart
listing: >
  one page per call. IsTruncated with NextToken feeds WithContinuationToken, so
  pagination stays explicit rather than hiding a request loop inside the call.
presigning: >
  Presign builds the URL as Get and Put would, including the path-style
  decision and the region a redirect last taught the client, then calls the
  query form of api:aws-signer. Contract in requirement:s3-client-scope
  presigning; reasoning in decision:s3-presign-in-scope.
scope: requirement:s3-client-scope
encoding: rule:sigv4-wire-agreement
client_policy: decision:http-client-policy-ownership
