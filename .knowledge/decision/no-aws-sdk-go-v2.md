---
id: decision:no-aws-sdk-go-v2
type: decision
title: S3 Access Is Hand-Written, Not aws-sdk-go-v2
---
`storage/s3` implements the S3 REST API directly instead of wrapping an existing Go client.

```yaml
state: accepted
accepted_on: 2026-07-28
measured_with: tinygo 0.41.1, aws-sdk-go-v2/service/s3 v1.106.0, smithy-go v1.27.3, minio-go v7
aws_sdk_go_v2:
  verdict: does not build under tinygo, and cannot be patched around
  blockers_in_dependency_order:
    - package: github.com/aws/smithy-go/transport/http
      cause: imports net/http/httputil, which itself does not compile under tinygo
    - package: github.com/aws/aws-sdk-go-v2/aws/retry
      cause: net.DNSError and net.OpError.Temporary() do not exist in tinygo
    - package: github.com/aws/aws-sdk-go-v2/aws/transport/http
      cause: >
        tinygo declares net/http.Transport as an empty struct, so DialContext,
        Clone, Proxy, TLSClientConfig, MaxIdleConns and ForceAttemptHTTP2 are all
        undefined, as are http.ProxyFromEnvironment, http.ErrUseLastResponse and
        net.Dialer.DualStack; 16 errors in one file
  why_injection_does_not_help: >
    service/s3 imports aws/transport/http directly, so supplying a custom
    aws.HTTPClient never removes that package from the build
  scale: 112 external packages in the closure, plus os/exec credential providers
minio_go:
  verdict: unusable
  build: x/net/publicsuffix pulls net/http/cookiejar, absent from tinygo
  maintenance: upstream development stopped, so a fork would not be carried
what_was_verified_instead:
  - sigv4 over crypto/hmac and crypto/sha256 matches aws-sdk-go-v2 signer/v4
  - encoding/xml marshal and unmarshal work, including time.Time fields
  - system:tinygo-netdev plus the https transport reach real AWS S3 over TLS
  - put, get, range get, head, list and delete pass against rustfs on both builds
consequences:
  - no external dependency enters go.mod
  - the API is this repository's own, see api:s3-client
  - multipart upload is out of scope, see requirement:s3-client-scope
related: requirement:no-crypto-tls-on-tinygo
