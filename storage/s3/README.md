# s3 — S3 client for TinyGo

The maintained Go clients do not build with TinyGo. `aws-sdk-go-v2` reaches for
the full `net/http.Transport` API, which TinyGo declares as an empty struct, and
its transport layer imports `net/http/httputil`, which does not compile under
TinyGo at all; `minio-go` fails even earlier, on `net/http/cookiejar`. This
package therefore speaks the S3 REST API directly, over the SigV4 signer in
[`cloud/aws`](../../cloud/aws), which [`nosql/dynamodb`](../../nosql/dynamodb)
shares.

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
| `Presign` | none; a SigV4 query-signed URL for GetObject, PutObject, HeadObject or DeleteObject |
| `CreateMultipartUpload`, `UploadPart`, `CompleteMultipartUpload`, `AbortMultipartUpload` | the multipart upload operations |

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

## Multipart upload

An object above the single-request limit goes up in parts. `CreateMultipartUpload`
fixes the object's content type and metadata and returns a `MultipartUpload`;
the other three calls take it back, so bucket, key and upload ID cannot drift
apart. Part numbers run from 1 to 10000, and every part but the last must be at
least 5 MiB, which the endpoint checks at completion.

```go
upload, err := client.CreateMultipartUpload(ctx, "bucket", "video.mp4",
	s3.WithContentType("video/mp4"))
var parts []s3.CompletedPart
for n := 1; ; n++ {
	chunk, done := nextChunk()
	part, err := client.UploadPart(ctx, *upload, n, chunk)
	if err != nil {
		client.AbortMultipartUpload(ctx, *upload)
		return err
	}
	parts = append(parts, *part)
	if done {
		break
	}
}
res, err := client.CompleteMultipartUpload(ctx, *upload, parts)
```

An upload that is neither completed nor aborted keeps its parts, and AWS bills
them, until a lifecycle rule removes it: abort on every failure path. A part
body follows `Put`'s rules, and `WithContentLength` is the one `Put` option
that applies to it. To let a browser send a part, presign it with the upload
ID and part number as query parameters:

```go
u, err := client.Presign(ctx, "bucket", "video.mp4", s3.PresignOptions{
	Method: "PUT",
	Query:  map[string]string{"uploadId": upload.UploadID, "partNumber": "3"},
})
```

## Presigned URLs

`Presign` returns a URL that authorizes one request without the credentials,
for a bounded time, so a browser can upload to or download from the bucket
directly and the application only issues the permission. It uses the client's
endpoint, region, addressing style and credentials, so a URL for RustFS, MinIO
or Cloudflare R2 needs no second configuration. No request is made.

```go
upload, err := client.Presign(ctx, "bucket", "uploads/cat.jpg", s3.PresignOptions{
	Method:      "PUT",
	Expires:     15 * time.Minute,
	ContentType: "image/jpeg",
})

download, err := client.Presign(ctx, "bucket", "uploads/cat.jpg", s3.PresignOptions{
	Query: map[string]string{"response-content-disposition": `attachment; filename="cat.jpg"`},
})
```

The signature carries `UNSIGNED-PAYLOAD`: the body is sent by someone who never
sees the credentials, so the signer cannot hash it. Only the host and the
headers the options name are signed, and each signed header is one the sender
must reproduce exactly. That is why `ContentType` and `Headers` constrain a
PUT, and why a GET names none: a link in a page cannot add a header, so the
`response-content-*` parameters go in `Query`, signed with the URL.

`Method` defaults to GET and `Expires` to fifteen minutes. S3 refuses more than
seven days, and so does `Presign`, with `ErrPresignExpiry`.

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
| `WithTimeout` | logical-operation timeout including redirects and response body, default 60s |
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
`ErrBucketNotEmpty`, `ErrInvalidRange`, `ErrBadCredentials`, `ErrNoSuchUpload`,
`ErrInvalidPart`, `ErrPresignExpiry`, `ErrNoCredentials`, `ErrNoRegion`,
`ErrTooManyRedirect`.

## Limitations

- **Put is one request.** The endpoint's single-request limit applies (5 GiB
  on AWS); anything larger goes through the multipart calls, and nothing here
  splits a stream into parts for you.
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
