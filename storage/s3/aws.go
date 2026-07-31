package s3

import (
	"net/http"
	"time"

	"github.com/shibukawa/tinygodriver/cloud/aws"
)

// Credentials are the values SigV4 signs with. It is an alias, so credentials
// built here work with any other client in this repository.
type Credentials = aws.Credentials

// CredentialsFromEnv reads AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY and
// AWS_SESSION_TOKEN.
func CredentialsFromEnv() Credentials { return aws.CredentialsFromEnv() }

// Backend identifies the HTTP stack selected by build constraints: "net/http"
// on standard Go builds, "https" on TinyGo builds.
const Backend = aws.Backend

// The signing constants this package passes to the signer.
const (
	emptyPayloadHash = aws.EmptyPayloadHash
	unsignedPayload  = aws.UnsignedPayload
)

func uriEncode(s string, encodeSlash bool) string { return aws.URIEncode(s, encodeSlash) }
func canonicalQuery(params [][2]string) string    { return aws.CanonicalQuery(params) }
func sha256Hex(b []byte) string                   { return aws.SHA256Hex(b) }

// sign signs req for S3 in region. S3 is the service that signs its path
// exactly as sent, which is why DoubleEncodePath stays false here; see the
// package comment of cloud/aws.
func sign(req *http.Request, creds Credentials, region, payloadHash string, now time.Time) {
	aws.Sign(req, creds, aws.SignRequest{
		Service:     "s3",
		Region:      region,
		PayloadHash: payloadHash,
		Time:        now,
	})
}

// newHTTPClient returns the client used when the caller supplies none.
//
// One idle connection per host is enough for object transfer, where a request
// carries a body large enough to dwarf the handshake it saves.
func newHTTPClient(timeout time.Duration) *http.Client {
	client := aws.NewHTTPClient(aws.ClientOptions{Timeout: timeout})
	aws.DisableRedirectFollowing(client)
	return client
}
