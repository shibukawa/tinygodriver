//go:build !tinygo

// These tests run under host Go. Without tags they exercise the standard-Go
// path; with -tags force_tinygo_logic they exercise the TinyGo path, so it is
// testable without a TinyGo toolchain.
package s3

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestSignAWSExample checks the signature against the GET Object example in the
// SigV4 documentation. The expected value was reproduced with
// aws-sdk-go-v2 aws/signer/v4 over the same request.
func TestSignAWSExample(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "https://examplebucket.s3.amazonaws.com/test.txt", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Range", "bytes=0-9")

	creds := Credentials{
		AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
		SecretAccessKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY",
	}
	sign(req, creds, "us-east-1", emptyPayloadHash, time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC))

	const want = "AWS4-HMAC-SHA256 " +
		"Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request, " +
		"SignedHeaders=host;range;x-amz-content-sha256;x-amz-date, " +
		"Signature=67fe34c8530db585abddc51067328adfedb6e42487d2566dc7d927d6e2722900"
	if got := req.Header.Get("Authorization"); got != want {
		t.Errorf("Authorization =\n %s\nwant\n %s", got, want)
	}
	if got := req.Header.Get("X-Amz-Date"); got != "20130524T000000Z" {
		t.Errorf("X-Amz-Date = %q", got)
	}
}

func TestSignIncludesSessionToken(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "https://bucket.s3.amazonaws.com/k", nil)
	if err != nil {
		t.Fatal(err)
	}
	creds := Credentials{AccessKeyID: "id", SecretAccessKey: "secret", SessionToken: "token"}
	sign(req, creds, "us-east-1", emptyPayloadHash, time.Now())

	if got := req.Header.Get("X-Amz-Security-Token"); got != "token" {
		t.Errorf("X-Amz-Security-Token = %q", got)
	}
	if got := req.Header.Get("Authorization"); !strings.Contains(got, "x-amz-security-token") {
		t.Errorf("session token not signed: %s", got)
	}
}

func TestURIEncode(t *testing.T) {
	for _, test := range []struct {
		name        string
		in          string
		encodeSlash bool
		want        string
	}{
		{"unreserved", "abcXYZ019-_.~", true, "abcXYZ019-_.~"},
		{"path keeps slash", "/a/b c", false, "/a/b%20c"},
		{"query escapes slash", "a/b", true, "a%2Fb"},
		{"parens are escaped", "2019 (a)", true, "2019%20%28a%29"},
		{"utf-8", "こん", true, "%E3%81%93%E3%82%93"},
		{"plus stays literal", "a+b", true, "a%2Bb"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := uriEncode(test.in, test.encodeSlash); got != test.want {
				t.Errorf("uriEncode(%q) = %q, want %q", test.in, got, test.want)
			}
		})
	}
}

func TestCanonicalQuery(t *testing.T) {
	got := canonicalQuery([][2]string{
		{"prefix", "photos/2019 (a)/"},
		{"list-type", "2"},
		{"uploads", ""},
	})
	const want = "list-type=2&prefix=photos%2F2019%20%28a%29%2F&uploads="
	if got != want {
		t.Errorf("canonicalQuery = %q, want %q", got, want)
	}
}

// TestEscapedPathMatchesSignature is the property the whole scheme rests on:
// what buildURL puts on the wire is what sign feeds into the canonical request.
func TestEscapedPathMatchesSignature(t *testing.T) {
	client := newTestClient(t, "https://s3.us-east-1.amazonaws.com")
	u := client.buildURL(&request{bucket: "bucket", key: "photos/2019 (a)/こん.txt"})

	const want = "/photos/2019%20%28a%29/%E3%81%93%E3%82%93.txt"
	if got := u.EscapedPath(); got != want {
		t.Errorf("EscapedPath = %q, want %q", got, want)
	}
	if got := u.RequestURI(); got != want {
		t.Errorf("RequestURI = %q, want %q", got, want)
	}
}

func TestBuildURLAddressing(t *testing.T) {
	for _, test := range []struct {
		name     string
		endpoint string
		options  []Option
		bucket   string
		key      string
		wantHost string
		wantPath string
	}{
		{
			name: "aws uses virtual host", endpoint: "https://s3.us-east-1.amazonaws.com",
			bucket: "b", key: "k",
			wantHost: "b.s3.us-east-1.amazonaws.com", wantPath: "/k",
		},
		{
			name: "custom endpoint uses path style", endpoint: "http://127.0.0.1:9000",
			bucket: "b", key: "k",
			wantHost: "127.0.0.1:9000", wantPath: "/b/k",
		},
		{
			name: "path style forced", endpoint: "https://s3.us-east-1.amazonaws.com",
			options: []Option{WithPathStyle(true)}, bucket: "b", key: "k",
			wantHost: "s3.us-east-1.amazonaws.com", wantPath: "/b/k",
		},
		{
			name: "bucket only", endpoint: "http://127.0.0.1:9000",
			bucket: "b", wantHost: "127.0.0.1:9000", wantPath: "/b",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := newTestClient(t, test.endpoint, test.options...)
			u := client.buildURL(&request{bucket: test.bucket, key: test.key})
			if u.Host != test.wantHost {
				t.Errorf("host = %q, want %q", u.Host, test.wantHost)
			}
			if u.Path != test.wantPath {
				t.Errorf("path = %q, want %q", u.Path, test.wantPath)
			}
		})
	}
}

func TestRetargetRegion(t *testing.T) {
	for _, test := range []struct {
		host   string
		region string
		want   string
		ok     bool
	}{
		{"s3.us-east-1.amazonaws.com", "eu-west-1", "s3.eu-west-1.amazonaws.com", true},
		{"bucket.s3.us-east-1.amazonaws.com", "eu-west-1", "bucket.s3.eu-west-1.amazonaws.com", true},
		{"s3.amazonaws.com", "eu-west-1", "s3.eu-west-1.amazonaws.com", true},
		{"127.0.0.1:9000", "eu-west-1", "", false},
	} {
		got, ok := retargetRegion(test.host, test.region)
		if ok != test.ok || got != test.want {
			t.Errorf("retargetRegion(%q) = %q, %v; want %q, %v", test.host, got, ok, test.want, test.ok)
		}
	}
}

func newTestClient(t *testing.T, endpoint string, opts ...Option) *Client {
	t.Helper()
	options := append([]Option{
		WithEndpoint(endpoint),
		WithRegion("us-east-1"),
		WithCredentials(Credentials{AccessKeyID: "id", SecretAccessKey: "secret"}),
	}, opts...)
	client, err := New(options...)
	if err != nil {
		t.Fatal(err)
	}
	return client
}
