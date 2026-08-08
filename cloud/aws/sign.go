package aws

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

const algorithm = "AWS4-HMAC-SHA256"

const (
	// EmptyPayloadHash is SHA-256 of no bytes, sent for bodyless requests.
	EmptyPayloadHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

	// UnsignedPayload signs a request without hashing the body, which is what
	// makes a non-rewindable stream uploadable. S3 accepts it; other services
	// do not, so it is not a general escape hatch.
	UnsignedPayload = "UNSIGNED-PAYLOAD"
)

// signedHeaderNames lists the headers included in the signature besides host
// and everything x-amz-*. A header here is signed only when the request
// carries it, so one list serves every service.
//
// Content-Length is absent on purpose: net/http writes it from
// Request.ContentLength rather than from the header map, so signing it would
// cover a header the server never receives.
var signedHeaderNames = []string{
	"content-encoding",
	"content-md5",
	"content-type",
	"range",
}

// SignRequest is everything about a signature that is not the request or the
// credentials.
type SignRequest struct {
	// Service is the name in the credential scope, "s3" or "dynamodb". It also
	// enters the signing key, and the two must agree: a mismatch is a
	// SignatureDoesNotMatch that no local test can catch, because both sides of
	// a self-test would use the same wrong value.
	Service string

	// Region is the signing region, "ap-northeast-1".
	Region string

	// PayloadHash is the hex SHA-256 of the body, EmptyPayloadHash, or
	// UnsignedPayload.
	PayloadHash string

	// DoubleEncodePath selects the canonicalization the SigV4 specification
	// applies to every service except S3, which signs the path exactly as sent.
	// A DynamoDB request posts to "/", where both rules agree, so this stays
	// false there too; a new service with a real path must set it.
	DoubleEncodePath bool

	// Time is the signing time. The zero value means now.
	Time time.Time
}

func hmacSHA256(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}

// SHA256Hex returns the hex SHA-256 of b, which is the form a payload hash
// takes on the wire.
func SHA256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// unreservedByte marks the bytes SigV4 leaves unescaped: ALPHA / DIGIT and
// "-", "_", ".", "~". A table lookup keeps URIEncode off the per-byte scan and
// the formatting machinery, since it runs over every key and query of every
// signed request.
var unreservedByte = func() (t [256]bool) {
	for c := 'A'; c <= 'Z'; c++ {
		t[c], t[c+'a'-'A'] = true, true
	}
	for c := '0'; c <= '9'; c++ {
		t[c] = true
	}
	t['-'], t['_'], t['.'], t['~'] = true, true, true, true
	return
}()

const upperhex = "0123456789ABCDEF"

// URIEncode escapes s the way SigV4 requires: every byte outside the unreserved
// set becomes %XX. Path segments keep their separators, query components do not.
//
// For S3 the encoded form is also what goes on the wire, so the signature covers
// the request line byte for byte.
func URIEncode(s string, encodeSlash bool) string {
	// The common case is a string that needs no escaping at all; return it
	// without copying.
	i := 0
	for i < len(s) && (unreservedByte[s[i]] || (s[i] == '/' && !encodeSlash)) {
		i++
	}
	if i == len(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 8)
	b.WriteString(s[:i])
	for ; i < len(s); i++ {
		c := s[i]
		switch {
		case unreservedByte[c]:
			b.WriteByte(c)
		case c == '/' && !encodeSlash:
			b.WriteByte('/')
		default:
			b.WriteByte('%')
			b.WriteByte(upperhex[c>>4])
			b.WriteByte(upperhex[c&0xF])
		}
	}
	return b.String()
}

// CanonicalQuery renders params sorted and escaped. A parameter with an empty
// value keeps its "=", which is what S3 subresources such as ?uploads expect.
func CanonicalQuery(params [][2]string) string {
	if len(params) == 0 {
		return ""
	}
	encoded := make([]string, 0, len(params))
	for _, p := range params {
		encoded = append(encoded, URIEncode(p[0], true)+"="+URIEncode(p[1], true))
	}
	sort.Strings(encoded)
	return strings.Join(encoded, "&")
}

// Sign adds x-amz-date, x-amz-content-sha256, the optional session token, and
// the Authorization header to req.
//
// req.URL.RawPath and req.URL.RawQuery must already hold the encoded forms
// produced by URIEncode and CanonicalQuery: the canonical request is built from
// them, so signature and request line cannot drift apart.
func Sign(req *http.Request, creds Credentials, sr SignRequest) {
	now := sr.Time
	if now.IsZero() {
		now = time.Now()
	}
	amzDate := now.UTC().Format("20060102T150405Z")
	dateStamp := amzDate[:8]

	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", sr.PayloadHash)
	if creds.SessionToken != "" {
		req.Header.Set("X-Amz-Security-Token", creds.SessionToken)
	}

	values := map[string]string{"host": host(req)}
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

	canonicalURI := req.URL.EscapedPath()
	if canonicalURI == "" {
		canonicalURI = "/"
	}
	if sr.DoubleEncodePath {
		canonicalURI = URIEncode(canonicalURI, false)
	}

	canonicalRequest := strings.Join([]string{
		req.Method,
		canonicalURI,
		req.URL.RawQuery,
		canonHeaders.String(),
		signedHeaders,
		sr.PayloadHash,
	}, "\n")

	scope := dateStamp + "/" + sr.Region + "/" + sr.Service + "/aws4_request"
	stringToSign := strings.Join([]string{
		algorithm,
		amzDate,
		scope,
		SHA256Hex([]byte(canonicalRequest)),
	}, "\n")

	key := signingKey(creds.SecretAccessKey, dateStamp, sr.Region, sr.Service)
	signature := hex.EncodeToString(hmacSHA256(key, stringToSign))

	req.Header.Set("Authorization", algorithm+" Credential="+creds.AccessKeyID+"/"+scope+
		", SignedHeaders="+signedHeaders+", Signature="+signature)
}

// signingKeyCache holds derived signing keys. The derivation is four chained
// HMACs whose inputs only change once a day (or when the region, service, or
// secret does), so a client issuing many small requests re-derives the same
// key on every one of them without this.
var signingKeyCache struct {
	sync.Mutex
	entries map[string][]byte
}

// signingKey derives (or recalls) the SigV4 signing key for one scope.
func signingKey(secret, dateStamp, region, service string) []byte {
	cacheKey := dateStamp + "/" + region + "/" + service + "/" + secret

	c := &signingKeyCache
	c.Lock()
	key, ok := c.entries[cacheKey]
	c.Unlock()
	if ok {
		return key
	}

	key = hmacSHA256(hmacSHA256(hmacSHA256(hmacSHA256(
		[]byte("AWS4"+secret), dateStamp), region), service), "aws4_request")

	c.Lock()
	// A stale-day entry is useless from midnight on; starting over also bounds
	// the map when credentials rotate often.
	if len(c.entries) >= 16 {
		c.entries = nil
	}
	if c.entries == nil {
		c.entries = make(map[string][]byte, 4)
	}
	c.entries[cacheKey] = key
	c.Unlock()
	return key
}

// host returns the host the request will send, which is Request.Host when the
// caller overrode it and the URL host otherwise. The signature covers the host
// header, so reading the wrong one produces a valid signature for a request
// nobody sent.
func host(req *http.Request) string {
	if req.Host != "" {
		return req.Host
	}
	return req.URL.Host
}
