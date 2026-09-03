// Package s3 is an S3 client that builds with TinyGo.
//
// The maintained Go clients cannot be used here. aws-sdk-go-v2 reaches for the
// full net/http.Transport API, which TinyGo declares as an empty struct, and
// its transport layer imports net/http/httputil, which does not compile under
// TinyGo at all. This package therefore speaks the S3 REST API directly: a
// SigV4 signer, request builders, and XML decoding, over whichever HTTP stack
// the build selects.
//
//	client, err := s3.New(
//		s3.WithRegion("ap-northeast-1"),
//		s3.WithCredentials(s3.Credentials{AccessKeyID: id, SecretAccessKey: secret}),
//	)
//	obj, err := client.Get(ctx, "bucket", "photos/cat.jpg")
//	defer obj.Body.Close()
//
// # Implementation selection
//
// Signing, request building, and response decoding are shared code: the two
// builds differ only in how a request reaches the network.
//
//   - standard Go builds use net/http and crypto/tls
//   - TinyGo builds use github.com/shibukawa/tinygodriver/https, which performs
//     TLS through the TLS stack of the host OS
//   - go build -tags force_tinygo_logic selects the TinyGo path under host Go,
//     which is how that path is tested without a TinyGo toolchain
//
// Neither build follows redirects through http.Client, because a redirected
// request must be signed again for its new host. Redirects are handled here
// instead, so a bucket in another region works the same way on both builds.
//
// # Credentials
//
// Credentials are static values or environment variables. There is no shared
// credentials file, no SSO, and no IMDS lookup:
//
//	s3.WithCredentials(s3.Credentials{AccessKeyID: id, SecretAccessKey: secret})
//	s3.WithCredentialsFromEnv() // AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY, ...
//
// New reads the environment when no credentials option is given.
//
// # Scope
//
// Put sends one request and is bounded by what the endpoint accepts in a
// single PUT (5 GiB on AWS). Above that, CreateMultipartUpload, UploadPart,
// CompleteMultipartUpload and AbortMultipartUpload are the operations; nothing
// here splits a stream into parts, since the caller knows the part boundaries
// and the failure policy.
//
// Presign is the one operation that makes no request: it returns a SigV4
// query-signed URL for a GET, PUT, HEAD or DELETE, so a browser can talk to the
// bucket directly for a bounded time while the application holds the
// credentials.
package s3
