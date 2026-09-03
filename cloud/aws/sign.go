package aws

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"sort"
	"strconv"
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
	amzDate, dateStamp := signingTime(sr.Time)

	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", sr.PayloadHash)
	if creds.SessionToken != "" {
		req.Header.Set("X-Amz-Security-Token", creds.SessionToken)
	}

	// The buffers are made here rather than in the helpers so escape analysis
	// keeps them on this frame: a request signs a handful of headers, and the
	// helpers only append.
	headers := collectHeaders(make([]signedHeader, 0, 8), req, false)
	signedHeaders := joinHeaderNames(make([]byte, 0, 96), headers)
	canonicalHex := canonicalRequestHash(make([]byte, 0, 512), req.Method,
		canonicalURI(req, sr.DoubleEncodePath), req.URL.RawQuery, headers, signedHeaders, sr.PayloadHash)

	scope := dateStamp + "/" + sr.Region + "/" + sr.Service + "/aws4_request"
	signature := signStringToSign(creds.SecretAccessKey, sr, amzDate, dateStamp, scope, canonicalHex)

	req.Header.Set("Authorization", algorithm+" Credential="+creds.AccessKeyID+"/"+scope+
		", SignedHeaders="+string(signedHeaders)+", Signature="+string(signature[:]))
}

// Presign signs req through its query string instead of its headers, so the
// URL alone authorizes the request until expires elapses: a presigned URL,
// which a browser can GET or PUT without the credentials. The X-Amz-Algorithm,
// X-Amz-Credential, X-Amz-Date, X-Amz-Expires, X-Amz-SignedHeaders and, with a
// session token, X-Amz-Security-Token parameters join req.URL.RawQuery in
// canonical order, and X-Amz-Signature is appended last.
//
// Presign sets no headers. Every header on req is signed instead, because each
// one is a header the eventual sender has to reproduce exactly, and only the
// caller knows which ones it will. sr.PayloadHash is normally UnsignedPayload:
// the body is sent by someone who never sees the credentials, so the signer
// cannot hash it. expires is rounded up to whole seconds; S3 accepts one second
// to seven days, and the caller checks that range.
//
// req.URL.RawPath and req.URL.RawQuery must hold the encoded forms produced by
// URIEncode and CanonicalQuery, as for Sign.
func Presign(req *http.Request, creds Credentials, sr SignRequest, expires time.Duration) {
	amzDate, dateStamp := signingTime(sr.Time)
	scope := dateStamp + "/" + sr.Region + "/" + sr.Service + "/aws4_request"

	headers := collectHeaders(make([]signedHeader, 0, 8), req, true)
	signedHeaders := joinHeaderNames(make([]byte, 0, 96), headers)

	// The authorization parameters are merged into the canonical query the
	// way CanonicalQuery would have placed them: encoded, then sorted. The
	// existing query is already in that form, so its pairs sort as they are.
	seconds := (expires + time.Second - 1) / time.Second
	pairs := make([]string, 0, 12)
	if req.URL.RawQuery != "" {
		pairs = append(pairs, strings.Split(req.URL.RawQuery, "&")...)
	}
	pairs = append(pairs,
		"X-Amz-Algorithm="+algorithm,
		"X-Amz-Credential="+URIEncode(creds.AccessKeyID+"/"+scope, true),
		"X-Amz-Date="+amzDate,
		"X-Amz-Expires="+strconv.FormatInt(int64(seconds), 10),
		"X-Amz-SignedHeaders="+URIEncode(string(signedHeaders), true),
	)
	if creds.SessionToken != "" {
		pairs = append(pairs, "X-Amz-Security-Token="+URIEncode(creds.SessionToken, true))
	}
	sort.Strings(pairs)
	query := strings.Join(pairs, "&")

	canonicalHex := canonicalRequestHash(make([]byte, 0, 512), req.Method,
		canonicalURI(req, sr.DoubleEncodePath), query, headers, signedHeaders, sr.PayloadHash)
	signature := signStringToSign(creds.SecretAccessKey, sr, amzDate, dateStamp, scope, canonicalHex)

	req.URL.RawQuery = query + "&X-Amz-Signature=" + string(signature[:])
}

// signingTime renders the signing time, now when t is zero, as the x-amz-date
// value and the date stamp of the credential scope.
func signingTime(t time.Time) (amzDate, dateStamp string) {
	if t.IsZero() {
		t = time.Now()
	}
	amzDate = t.UTC().Format("20060102T150405Z")
	return amzDate, amzDate[:8]
}

// collectHeaders appends to headers the headers the signature covers, sorted
// by lowercase name: host always, then either every header on the request
// (all) or the signedHeaderNames present plus everything x-amz-*.
//
// A small sorted slice instead of a map plus sort.Strings: signing runs on
// every request, and the map, the key slice, and the per-key ToLower were its
// allocations.
func collectHeaders(headers []signedHeader, req *http.Request, all bool) []signedHeader {
	headers = insertHeader(headers, "host", host(req))
	if all {
		for name, vs := range req.Header {
			if len(vs) > 0 {
				headers = insertHeader(headers, lowerHeaderName(name), vs[0])
			}
		}
		return headers
	}
	for _, name := range signedHeaderNames {
		if v := req.Header.Get(name); v != "" {
			headers = insertHeader(headers, name, v)
		}
	}
	for name, vs := range req.Header {
		if hasXAmzPrefix(name) && len(vs) > 0 {
			headers = insertHeader(headers, lowerHeaderName(name), vs[0])
		}
	}
	return headers
}

// joinHeaderNames appends the SignedHeaders list, "host;range;x-amz-date".
func joinHeaderNames(signedHeaders []byte, headers []signedHeader) []byte {
	for i, h := range headers {
		if i > 0 {
			signedHeaders = append(signedHeaders, ';')
		}
		signedHeaders = append(signedHeaders, h.name...)
	}
	return signedHeaders
}

// canonicalURI is the path the canonical request names: the escaped path as
// sent, or its double-encoded form for the services that normalize it.
func canonicalURI(req *http.Request, doubleEncode bool) string {
	uri := req.URL.EscapedPath()
	if uri == "" {
		uri = "/"
	}
	if doubleEncode {
		uri = URIEncode(uri, false)
	}
	return uri
}

// canonicalRequestHash assembles the canonical request into canonical and
// returns its hex SHA-256. The request exists only to be hashed, so it is built
// in one buffer and never becomes a string.
func canonicalRequestHash(canonical []byte, method, uri, query string, headers []signedHeader, signedHeaders []byte, payloadHash string) [sha256.Size * 2]byte {
	canonical = append(canonical, method...)
	canonical = append(canonical, '\n')
	canonical = append(canonical, uri...)
	canonical = append(canonical, '\n')
	canonical = append(canonical, query...)
	canonical = append(canonical, '\n')
	for _, h := range headers {
		canonical = append(canonical, h.name...)
		canonical = append(canonical, ':')
		canonical = append(canonical, strings.TrimSpace(h.value)...)
		canonical = append(canonical, '\n')
	}
	canonical = append(canonical, '\n')
	canonical = append(canonical, signedHeaders...)
	canonical = append(canonical, '\n')
	canonical = append(canonical, payloadHash...)
	canonicalSum := sha256.Sum256(canonical)
	var canonicalHex [sha256.Size * 2]byte
	hex.Encode(canonicalHex[:], canonicalSum[:])
	return canonicalHex
}

// signStringToSign builds the string to sign over the canonical request hash
// and returns its hex signature under the derived key.
func signStringToSign(secret string, sr SignRequest, amzDate, dateStamp, scope string, canonicalHex [sha256.Size * 2]byte) [sha256.Size * 2]byte {
	stringToSign := make([]byte, 0, len(algorithm)+len(amzDate)+len(scope)+len(canonicalHex)+3)
	stringToSign = append(stringToSign, algorithm...)
	stringToSign = append(stringToSign, '\n')
	stringToSign = append(stringToSign, amzDate...)
	stringToSign = append(stringToSign, '\n')
	stringToSign = append(stringToSign, scope...)
	stringToSign = append(stringToSign, '\n')
	stringToSign = append(stringToSign, canonicalHex[:]...)

	key := signingKey(secret, dateStamp, sr.Region, sr.Service)
	mac := hmac.New(sha256.New, key)
	mac.Write(stringToSign)
	var signature [sha256.Size * 2]byte
	hex.Encode(signature[:], mac.Sum(nil))
	return signature
}

// signedHeader is one canonical header: a lowercase name and its raw value.
type signedHeader struct {
	name  string
	value string
}

// insertHeader keeps hs sorted by name, replacing the value when the name is
// already present — which is what the map this replaces did. The slice never
// holds more than a handful of entries, so insertion sort is the whole
// algorithm.
func insertHeader(hs []signedHeader, name, value string) []signedHeader {
	i := 0
	for i < len(hs) && hs[i].name < name {
		i++
	}
	if i < len(hs) && hs[i].name == name {
		hs[i].value = value
		return hs
	}
	hs = append(hs, signedHeader{})
	copy(hs[i+1:], hs[i:])
	hs[i] = signedHeader{name: name, value: value}
	return hs
}

// hasXAmzPrefix reports whether name starts with "x-amz-" in any case, without
// lowering the whole name first.
func hasXAmzPrefix(name string) bool {
	const prefix = "x-amz-"
	if len(name) < len(prefix) {
		return false
	}
	for i := 0; i < len(prefix); i++ {
		c := name[i]
		if 'A' <= c && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != prefix[i] {
			return false
		}
	}
	return true
}

// lowerHeaderName lowercases a header name, answering the names this package
// itself sets from constants so the ordinary request allocates nothing here.
func lowerHeaderName(name string) string {
	switch name {
	case "X-Amz-Date":
		return "x-amz-date"
	case "X-Amz-Content-Sha256":
		return "x-amz-content-sha256"
	case "X-Amz-Security-Token":
		return "x-amz-security-token"
	case "X-Amz-Target":
		return "x-amz-target"
	}
	return strings.ToLower(name)
}

// signingKeyCache holds derived signing keys. The derivation is four chained
// HMACs whose inputs only change once a day (or when the region, service, or
// secret does), so a client issuing many small requests re-derives the same
// key on every one of them without this.
var signingKeyCache struct {
	sync.Mutex
	entries map[signingScope][]byte
}

// signingScope identifies one derived key. A comparable struct rather than a
// concatenated string: the concatenation allocated per call, and it spliced
// the secret into a new string on every signature.
type signingScope struct {
	dateStamp, region, service, secret string
}

// signingKey derives (or recalls) the SigV4 signing key for one scope.
func signingKey(secret, dateStamp, region, service string) []byte {
	cacheKey := signingScope{dateStamp: dateStamp, region: region, service: service, secret: secret}

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
		c.entries = make(map[signingScope][]byte, 4)
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
