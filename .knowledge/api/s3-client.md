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
  - func (c *Client) Region() string
  - func (c *Client) Endpoint() string
options:
  client: WithEndpoint, WithRegion, WithCredentials, WithCredentialsFromEnv, WithPathStyle, WithUnsignedPayload, WithTimeout, WithHTTPClient
  put: WithContentType, WithContentEncoding, WithContentLength, WithMetadata
  list: WithPrefix, WithDelimiter, WithMaxKeys, WithStartAfter, WithContinuationToken
defaults:
  credentials: CredentialsFromEnv when no credentials option is given
  region: AWS_REGION, then AWS_DEFAULT_REGION; absent is ErrNoRegion
  endpoint: AWS_ENDPOINT_URL_S3, then AWS_ENDPOINT_URL, then https://s3.<region>.amazonaws.com
  addressing: virtual host for amazonaws.com, path style elsewhere
  timeout: 60s
implementation:
  shared: signing, request building, XML decoding
  std_go: net/http with crypto/tls, Backend == "net/http"
  tinygo: api:https-transport, Backend == "https"
  redirects: requirement:s3-redirect-resigning
errors:
  type: "*s3.Error with Op, Bucket, Key, StatusCode, Code, Message, RequestID"
  sentinels: ErrNoSuchKey, ErrNoSuchBucket, ErrAccessDenied, ErrBucketExists, ErrBucketNotEmpty, ErrInvalidRange, ErrBadCredentials, ErrNoCredentials, ErrNoRegion, ErrTooManyRedirect
  mapping: the Code element first, then the status code when there is no error document
listing: >
  one page per call. IsTruncated with NextToken feeds WithContinuationToken, so
  pagination stays explicit rather than hiding a request loop inside the call.
scope: requirement:s3-client-scope
encoding: rule:sigv4-wire-agreement
