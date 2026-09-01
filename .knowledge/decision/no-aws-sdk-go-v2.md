---
id: decision:no-aws-sdk-go-v2
type: decision
title: AWS Access Is Hand-Written, Not aws-sdk-go-v2
---
`storage/s3` and `nosql/dynamodb` implement the AWS REST APIs directly instead of wrapping an existing Go client.

```yaml
state: accepted
accepted_on: 2026-07-28
measured_with: tinygo 0.41.1, aws-sdk-go-v2/service/s3 v1.106.0, smithy-go v1.27.3, minio-go v7
aws_sdk_go_v2:
  verdict: >
    did not build under tinygo 0.41. Re-measured on 0.42: two of the three
    blockers are gone and one method stands in the way. The decision holds on
    other grounds -- see would_it_work_at_0_42 -- but the build argument is
    nearly spent and must be re-checked, not quoted
  blockers_in_dependency_order:
    - package: github.com/aws/smithy-go/transport/http
      cause: imports net/http/httputil, which itself does not compile under tinygo
      status_0_42: gone; tinygo 0.42 ships net/http/httputil and it compiles
    - package: github.com/aws/aws-sdk-go-v2/aws/retry
      cause: net.DNSError and net.OpError.Temporary() do not exist in tinygo
      status_0_42: >
        the only blocker left, and it is one method. net.DNSError exists now;
        net.OpError has Unwrap, Error and Timeout but still no Temporary
    - package: github.com/aws/aws-sdk-go-v2/aws/transport/http
      cause: >
        tinygo declares net/http.Transport as an empty struct, so DialContext,
        Clone, Proxy, TLSClientConfig, MaxIdleConns and ForceAttemptHTTP2 are all
        undefined, as are http.ProxyFromEnvironment, http.ErrUseLastResponse and
        net.Dialer.DualStack; 16 errors in one file
      status_0_42: >
        gone except DualStack, which tinygo omits on purpose. Transport now
        carries the fields
  measured_2026_09_02:
    versions: service/s3 v1.110.0, aws-sdk-go-v2 v1.45.1, smithy-go v1.28.1
    tinygo: 0.42.0 darwin/arm64
    result: >
      exactly one error, aws/retry/retryable_error.go:140:57 "netOpErr.Temporary
      undefined (type *net.OpError has no field or method Temporary)"
  would_it_work_at_0_42: >
    compiling is not the bar. tinygo 0.42's Transport fields are, in its own
    words, "stored but ... have no effect at runtime", and Proxy is "present for
    API compatibility". A built SDK would silently ignore TLSClientConfig, and
    requirement:no-crypto-tls-on-tinygo records that its crypto/tls hands back
    plaintext. So a green build would be the start of the evaluation, not the
    end of it
  why_injection_does_not_help: >
    service/s3 imports aws/transport/http directly, so supplying a custom
    aws.HTTPClient never removes that package from the build
  scale: 112 external packages in the closure, plus os/exec credential providers
  dynamodb_service:
    measured_on: 2026-07-30
    versions: service/dynamodb v1.62.2, smithy-go v1.27.5
    verdict: fails at the same first blocker, net/http/httputil
    scale: 244 packages, including service/internal/endpoint-discovery
    finding: >
      the blocker is the shared transport layer, so it is per-SDK rather than
      per-service. No AWS service client from this SDK can build under tinygo.
    detail: requirement:dynamodb-driver-validation
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
  - the API is this repository's own, see api:s3-client and api:dynamodb-client
  - multipart upload is out of scope, see requirement:s3-client-scope
  - the signer is written once and shared, see decision:aws-shared-package
related: requirement:no-crypto-tls-on-tinygo
