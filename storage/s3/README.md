# s3 — S3 client for TinyGo

The maintained Go clients do not build with TinyGo. `aws-sdk-go-v2` reaches for
the full `net/http.Transport` API, which TinyGo declares as an empty struct, and
its transport layer imports `net/http/httputil`, which does not compile under
TinyGo at all; `minio-go` fails even earlier, on `net/http/cookiejar`. This
package therefore speaks the S3 REST API directly.

```go
import "github.com/shibukawa/tinygodriver/storage/s3"

client, err := s3.New(
	s3.WithRegion("ap-northeast-1"),
	s3.WithCredentials(s3.Credentials{AccessKeyID: id, SecretAccessKey: secret}),
)

obj, err := client.Get(ctx, "bucket", "photos/cat.jpg")
defer obj.Body.Close()
```

## Implementation selection

Signing, request building, and XML decoding are shared code. The builds differ
only in how a request reaches the network:

| Build | HTTP stack (`s3.Backend`) |
| --- | --- |
| Standard Go | `net/http` with `crypto/tls` |
| TinyGo, or `-tags force_tinygo_logic` | [`https`](../../https), TLS through the host OS |

Neither build lets `http.Client` follow redirects, because a redirected request
goes to a different host and its signature covers that host. `Client` follows
them itself and signs each hop, so a bucket in another region works the same on
both builds. TinyGo's `http.Client` never follows redirects anyway, which is
what makes this the only correct design.

## API

| Method | S3 operation |
| --- | --- |
| `Get`, `GetRange` | GetObject, with `Range` |
| `Put` | PutObject |
| `Head` | HeadObject |
| `Delete` | DeleteObject |
| `List` | ListObjectsV2, one page |
| `CreateBucket`, `DeleteBucket` | CreateBucket, DeleteBucket |

`Put` takes `WithContentType`, `WithContentEncoding`, `WithMetadata`, and
`WithContentLength`. `List` takes `WithPrefix`, `WithDelimiter`, `WithMaxKeys`,
`WithStartAfter`, and `WithContinuationToken`.

`List` returns one page. A truncated page carries `NextToken`:

```go
var token string
for {
	page, err := client.List(ctx, "bucket",
		s3.WithPrefix("photos/"), s3.WithContinuationToken(token))
	if err != nil {
		return err
	}
	for _, obj := range page.Objects {
		fmt.Println(obj.Key, obj.Size)
	}
	if !page.IsTruncated {
		break
	}
	token = page.NextToken
}
```

## Configuration

`New` falls back to the environment, so a configured shell needs no options:
`AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN`, `AWS_REGION`
(or `AWS_DEFAULT_REGION`), and `AWS_ENDPOINT_URL_S3` (or `AWS_ENDPOINT_URL`).

| Option | Effect |
| --- | --- |
| `WithEndpoint` | endpoint URL, for S3-compatible servers |
| `WithRegion` | signing region |
| `WithCredentials` | static credentials |
| `WithCredentialsFromEnv` | read credentials from the environment |
| `WithPathStyle` | `endpoint/bucket/key` instead of `bucket.endpoint/key` |
| `WithUnsignedPayload` | sign headers only, so large streams are not buffered |
| `WithTimeout` | per-request timeout, default 60s |
| `WithHTTPClient` | supply the `http.Client` |

Addressing defaults to virtual-host style for `amazonaws.com` endpoints and
path style everywhere else, which is what S3-compatible servers expect.

```go
client, err := s3.New(
	s3.WithEndpoint("http://127.0.0.1:9000"),
	s3.WithRegion("us-east-1"),
	s3.WithCredentials(s3.Credentials{AccessKeyID: "admin", SecretAccessKey: "admin"}),
)
```

## Errors

S3 error codes map onto sentinels, so application code branches without
matching strings. `*s3.Error` carries the status, code, message, and request ID.

```go
obj, err := client.Get(ctx, "bucket", "missing")
if errors.Is(err, s3.ErrNoSuchKey) {
	// ...
}

var s3err *s3.Error
if errors.As(err, &s3err) {
	log.Println(s3err.StatusCode, s3err.Code, s3err.RequestID)
}
```

`ErrNoSuchKey`, `ErrNoSuchBucket`, `ErrAccessDenied`, `ErrBucketExists`,
`ErrBucketNotEmpty`, `ErrInvalidRange`, `ErrBadCredentials`, `ErrNoCredentials`,
`ErrNoRegion`, `ErrTooManyRedirect`.

## Limitations

- **No multipart upload.** `Put` sends one request, so the endpoint's
  single-request limit applies (5 GiB on AWS).
- **Put reads the body twice.** SigV4 signs a hash of the payload. A body that
  implements `io.Seeker` is hashed and rewound; anything else is buffered in
  memory. `WithUnsignedPayload` streams instead, at the cost of the signature no
  longer covering the body — use it only over https. A stream that reports no
  length goes out chunked, which AWS rejects for PutObject, so pass
  `s3.WithContentLength(n)` with it.
- **No connection reuse on TinyGo.** The `https` transport opens a connection
  per request, so every call pays a TLS handshake.
- **Credentials are static or environment values.** No shared credentials file,
  no SSO, no IMDS.
- **No versioning, ACL, tagging, or lifecycle APIs.**

## Testing

Unit tests run against a fake endpoint under both build configurations:

```bash
go test ./storage/s3/
```

```bash
go test -tags force_tinygo_logic ./storage/s3/
```

Integration tests need an S3-compatible endpoint, for example
[RustFS](https://rustfs.com/):

```bash
docker run -d --name rustfs -p 19000:9000 -e RUSTFS_ACCESS_KEY=rustfsadmin -e RUSTFS_SECRET_KEY=rustfsadmin -e RUSTFS_VOLUMES=/data rustfs/rustfs
```

```bash
S3_TEST_ENDPOINT=http://127.0.0.1:19000 S3_TEST_ACCESS_KEY=rustfsadmin S3_TEST_SECRET_KEY=rustfsadmin go test ./storage/s3/
```
