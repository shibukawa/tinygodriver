package aws

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// TestPresignS3Example checks the query-string signature against the presigned
// GET Object example in the S3 documentation ("Authenticating Requests: Using
// Query Parameters"), the one published vector for this form. That page's
// example secret has a slash where the general SigV4 example has a plus; the
// expected signature is the page's, so the secret must be too.
func TestPresignS3Example(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "https://examplebucket.s3.amazonaws.com/test.txt", nil)
	if err != nil {
		t.Fatal(err)
	}
	creds := Credentials{
		AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
		SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	}
	Presign(req, creds, SignRequest{
		Service:     "s3",
		Region:      "us-east-1",
		PayloadHash: UnsignedPayload,
		Time:        time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC),
	}, 24*time.Hour)

	const want = "X-Amz-Algorithm=AWS4-HMAC-SHA256" +
		"&X-Amz-Credential=AKIAIOSFODNN7EXAMPLE%2F20130524%2Fus-east-1%2Fs3%2Faws4_request" +
		"&X-Amz-Date=20130524T000000Z&X-Amz-Expires=86400&X-Amz-SignedHeaders=host" +
		"&X-Amz-Signature=aeeed9bbccd4d02ee5c0109b86d86835f995330da4c265957d157751f604d404"
	if got := req.URL.RawQuery; got != want {
		t.Errorf("query =\n %s\nwant\n %s", got, want)
	}
	if len(req.Header) != 0 {
		t.Errorf("Presign set headers: %v", req.Header)
	}
}

// The pinned cases cover what the documented example does not: an existing
// query merged in canonical order, a session token, and headers outside the
// Sign whitelist, which Presign signs because the caller put them there.
func TestPresignPinned(t *testing.T) {
	creds := Credentials{
		AccessKeyID:     "AKIDEXAMPLE",
		SecretAccessKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY",
	}
	credsWithToken := creds
	credsWithToken.SessionToken = "IQoJb3JpZ2luX2VjEXAMPLETOKEN"
	when := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

	build := func(method, host, path string, params [][2]string, header map[string]string) *http.Request {
		target := &url.URL{
			Scheme:   "https",
			Host:     host,
			Path:     path,
			RawPath:  URIEncode(path, false),
			RawQuery: CanonicalQuery(params),
		}
		req := &http.Request{Method: method, URL: target, Host: host, Header: http.Header{}}
		for name, value := range header {
			req.Header.Set(name, value)
		}
		return req
	}

	tests := []struct {
		name    string
		req     *http.Request
		cr      Credentials
		expires time.Duration
		want    string
	}{
		{
			name: "put with content-type, metadata, and a part query",
			req: build(http.MethodPut, "bench-bucket.s3.ap-northeast-1.amazonaws.com",
				"/sensor readings/2026/08/01.json",
				[][2]string{{"partNumber", "1"}, {"uploadId", "abc123"}},
				map[string]string{
					"Content-Type":        "application/json",
					"Content-Disposition": `attachment; filename="01.json"`,
					"X-Amz-Meta-Sensor":   "room-1",
				}),
			cr:      creds,
			expires: 15 * time.Minute,
			want: "X-Amz-Algorithm=AWS4-HMAC-SHA256" +
				"&X-Amz-Credential=AKIDEXAMPLE%2F20260801%2Fap-northeast-1%2Fs3%2Faws4_request" +
				"&X-Amz-Date=20260801T120000Z&X-Amz-Expires=900" +
				"&X-Amz-SignedHeaders=content-disposition%3Bcontent-type%3Bhost%3Bx-amz-meta-sensor" +
				"&partNumber=1&uploadId=abc123" +
				"&X-Amz-Signature=1061298f91b6523f49a01ec3aba313e9c6482480ae3698430d3529e3d7b6dd4c",
		},
		{
			name: "get with session token and response headers as query",
			req: build(http.MethodGet, "127.0.0.1:9000", "/bucket/a/b~c.txt",
				[][2]string{{"response-content-disposition", `attachment; filename="c.txt"`}}, nil),
			cr:      credsWithToken,
			expires: 1500 * time.Millisecond,
			want: "X-Amz-Algorithm=AWS4-HMAC-SHA256" +
				"&X-Amz-Credential=AKIDEXAMPLE%2F20260801%2Fap-northeast-1%2Fs3%2Faws4_request" +
				"&X-Amz-Date=20260801T120000Z&X-Amz-Expires=2" +
				"&X-Amz-Security-Token=IQoJb3JpZ2luX2VjEXAMPLETOKEN&X-Amz-SignedHeaders=host" +
				"&response-content-disposition=attachment%3B%20filename%3D%22c.txt%22" +
				"&X-Amz-Signature=192b9eb25f06a4e8f320be72518e648cd90ebfe7e239ba962474f1ec014c1065",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			Presign(tt.req, tt.cr, SignRequest{Service: "s3", Region: "ap-northeast-1",
				PayloadHash: UnsignedPayload, Time: when}, tt.expires)
			got := tt.req.URL.RawQuery
			if got != tt.want {
				t.Errorf("query drifted:\n got %s\nwant %s", got, tt.want)
			}
			if !strings.HasSuffix(got[:len(got)-64], "&X-Amz-Signature=") {
				t.Error("signature is not the last parameter")
			}
		})
	}
}

// A bare request signs host alone, and the header-form names Sign adds never
// appear in the query form: each signed header is one the sender must
// reproduce, and a browser sends neither x-amz-date nor x-amz-content-sha256.
func TestPresignSignedHeadersAreOnlyWhatTheSenderSends(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPut, "https://b.s3.amazonaws.com/k", nil)
	Presign(req, Credentials{AccessKeyID: "id", SecretAccessKey: "secret"},
		SignRequest{Service: "s3", Region: "us-east-1", PayloadHash: UnsignedPayload}, time.Minute)
	if !strings.Contains(req.URL.RawQuery, "&X-Amz-SignedHeaders=host&") {
		t.Errorf("a bare request must sign host alone: %s", req.URL.RawQuery)
	}
	if strings.Contains(req.URL.RawQuery, "x-amz-date") || strings.Contains(req.URL.RawQuery, "x-amz-content-sha256") {
		t.Errorf("header-form names leaked into the query form: %s", req.URL.RawQuery)
	}
}
