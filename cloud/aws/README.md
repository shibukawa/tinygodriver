# aws — SigV4 signing and credentials for TinyGo

What every AWS service client in this repository needs: SigV4 signing,
credentials, environment resolution, and the HTTP client the build selects.
[`storage/s3`](../../storage/s3) and [`nosql/dynamodb`](../../nosql/dynamodb)
are built on it.

It exists because `aws-sdk-go-v2` does not build with TinyGo, so the services
here speak their REST APIs directly. Signing is the part they share, and a
second copy of it would be a second chance to break the rule that the signature
must cover exactly what goes on the wire.

```go
import "github.com/shibukawa/tinygodriver/cloud/aws"

req, _ := http.NewRequest("POST", endpoint, bytes.NewReader(payload))
req.Header.Set("Content-Type", "application/x-amz-json-1.0")

aws.Sign(req, creds, aws.SignRequest{
	Service:     "dynamodb",
	Region:      "ap-northeast-1",
	PayloadHash: aws.SHA256Hex(payload),
})
```

## Signing another service

The request is an ordinary `*http.Request` and `Sign` only reads and sets
headers, so a service with no client here is still reachable. Two rules matter.

**The service name is not decoration.** It enters both the credential scope and
the signing key, and the two must agree. A mismatch is a `SignatureDoesNotMatch`
that no local test catches, because both sides of a self-test would use the same
wrong value.

**S3 is the exception, not the template.** SigV4 normalizes and double-encodes
the request path for every service except S3, which signs the path exactly as
sent. Set `DoubleEncodePath` for anything with a path other than `/`:

```go
aws.Sign(req, creds, aws.SignRequest{
	Service: "lambda", Region: region, PayloadHash: hash,
	DoubleEncodePath: true,
})
```

DynamoDB posts to `/`, where both rules agree, which is why the flag stays false
there.

## Presigned URLs

`Presign` is the same signer with the other output: the credential scope, date,
expiry and signed-header list go into the query string as `X-Amz-*` parameters
and the signature is appended as `X-Amz-Signature`, so the URL alone authorizes
the request until it expires. It sets no headers and signs every header on the
request, because each signed header is one the eventual sender has to
reproduce. The payload hash is normally `UnsignedPayload`, the body being sent
by someone who never sees the credentials.

```go
aws.Presign(req, creds, aws.SignRequest{
	Service: "s3", Region: region, PayloadHash: aws.UnsignedPayload,
}, 15*time.Minute)
url := req.URL.String()
```

`storage/s3` wraps it as `Client.Presign`.

## Credentials

```go
creds := aws.CredentialsFromEnv() // AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY, AWS_SESSION_TOKEN
creds := aws.Credentials{AccessKeyID: id, SecretAccessKey: secret}
```

Static values or environment variables. There is no shared credentials file, no
SSO, and no metadata service: those need an INI parser, a browser flow, and a
link-local HTTP call respectively, none of which belong in a client this size.

`RegionFromEnv` reads `AWS_REGION` then `AWS_DEFAULT_REGION`.
`EndpointFromEnv(service)` reads `AWS_ENDPOINT_URL_<SERVICE>` then
`AWS_ENDPOINT_URL`, the names the AWS CLI uses.

## The HTTP client

`NewHTTPClient` returns the client a service package uses when the caller
supplies none. It selects the transport by build tag and carries the same
idle-connection setting to both:

```go
client := aws.NewHTTPClient(aws.ClientOptions{
	Timeout:             10 * time.Second,
	MaxIdleConnsPerHost: 4,
})
```

| Build | Transport (`aws.Backend`) |
| --- | --- |
| Standard Go | `net/http` with `crypto/tls` |
| TinyGo, or `-tags force_tinygo_logic` | [`https`](../../https), TLS through the host OS |

`DisableRedirectFollowing` stops `http.Client` from following redirects, which a
signed request needs: the signature covers the host header, so a redirected
request has to be signed again for its new host. `storage/s3` does that itself.

## What is not here

No credential chain, no retry policy, no endpoint resolution rules, and no
request models. Retrying is per-service — DynamoDB must retry throttling, S3
mostly should not — so it lives with the service that knows what its errors
mean.

## Testing

```bash
go test ./cloud/aws/ && go test -tags force_tinygo_logic ./cloud/aws/
```

The signature tests are known-answer tests. The S3 header case is the example
from the SigV4 documentation and the presigned case is the query-parameter
example from the S3 documentation; the DynamoDB cases were produced by
`aws-sdk-go-v2`'s own signer over the same request and the same header set,
which is what makes them evidence rather than a restatement of this
implementation.
