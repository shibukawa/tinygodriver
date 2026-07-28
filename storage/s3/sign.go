package s3

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

const (
	algorithm = "AWS4-HMAC-SHA256"

	// emptyPayloadHash is SHA-256 of no bytes, sent for bodyless requests.
	emptyPayloadHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

	// unsignedPayload signs the request without hashing the body. S3 accepts
	// it, which is what makes a non-rewindable stream uploadable.
	unsignedPayload = "UNSIGNED-PAYLOAD"
)

// signedHeaderNames lists the headers included in the signature besides host
// and everything x-amz-*. Content-Length is absent on purpose: net/http writes
// it from Request.ContentLength rather than from the header map.
var signedHeaderNames = []string{
	"content-encoding",
	"content-md5",
	"content-type",
	"range",
}

func hmacSHA256(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// uriEncode escapes s the way SigV4 requires: every byte outside the unreserved
// set becomes %XX. Path segments keep their separators, query components do not.
//
// The encoded form is also what goes on the wire, so the signature covers the
// request line byte for byte.
func uriEncode(s string, encodeSlash bool) string {
	const unreserved = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_.~"
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case strings.IndexByte(unreserved, c) >= 0:
			b.WriteByte(c)
		case c == '/' && !encodeSlash:
			b.WriteByte('/')
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// canonicalQuery renders params sorted and escaped. A parameter with an empty
// value keeps its "=", which is what S3 subresources such as ?uploads expect.
func canonicalQuery(params [][2]string) string {
	if len(params) == 0 {
		return ""
	}
	encoded := make([]string, 0, len(params))
	for _, p := range params {
		encoded = append(encoded, uriEncode(p[0], true)+"="+uriEncode(p[1], true))
	}
	sort.Strings(encoded)
	return strings.Join(encoded, "&")
}

// sign adds x-amz-date, x-amz-content-sha256, the optional session token, and
// the Authorization header to req.
//
// req.URL.RawPath and req.URL.RawQuery must already hold the encoded forms
// produced by uriEncode and canonicalQuery: the canonical request is built from
// them, so signature and request line cannot drift apart.
func sign(req *http.Request, creds Credentials, region, payloadHash string, now time.Time) {
	amzDate := now.UTC().Format("20060102T150405Z")
	dateStamp := amzDate[:8]

	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	if creds.SessionToken != "" {
		req.Header.Set("X-Amz-Security-Token", creds.SessionToken)
	}

	values := map[string]string{"host": req.URL.Host}
	for _, name := range signedHeaderNames {
		if v := req.Header.Get(name); v != "" {
			values[name] = v
		}
	}
	for name, vs := range req.Header {
		lower := strings.ToLower(name)
		if strings.HasPrefix(lower, "x-amz-") && len(vs) > 0 {
			values[lower] = vs[0]
		}
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)

	var canonHeaders strings.Builder
	for _, name := range names {
		canonHeaders.WriteString(name)
		canonHeaders.WriteByte(':')
		canonHeaders.WriteString(strings.TrimSpace(values[name]))
		canonHeaders.WriteByte('\n')
	}
	signedHeaders := strings.Join(names, ";")

	canonicalRequest := strings.Join([]string{
		req.Method,
		req.URL.EscapedPath(),
		req.URL.RawQuery,
		canonHeaders.String(),
		signedHeaders,
		payloadHash,
	}, "\n")

	scope := dateStamp + "/" + region + "/s3/aws4_request"
	stringToSign := strings.Join([]string{
		algorithm,
		amzDate,
		scope,
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")

	key := hmacSHA256(hmacSHA256(hmacSHA256(hmacSHA256(
		[]byte("AWS4"+creds.SecretAccessKey), dateStamp), region), "s3"), "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(key, stringToSign))

	req.Header.Set("Authorization", fmt.Sprintf("%s Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		algorithm, creds.AccessKeyID, scope, signedHeaders, signature))
}
