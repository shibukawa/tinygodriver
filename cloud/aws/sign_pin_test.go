package aws

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// These pin the exact Authorization header for a handful of fixed requests.
// The values were produced by this package and cross-checked against the shape
// SigV4 documents; any change to the canonical request construction that
// alters one of them is a signing break, not a refactor.
func TestSignPinnedAuthorization(t *testing.T) {
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
		name string
		req  *http.Request
		cr   Credentials
		sr   SignRequest
		want string
	}{
		{
			name: "s3 put with metadata and query",
			req: build(http.MethodPut, "bench-bucket.s3.ap-northeast-1.amazonaws.com",
				"/sensor readings/2026/08/01.json",
				[][2]string{{"partNumber", "1"}, {"uploadId", "abc123"}},
				map[string]string{
					"Content-Type":      "application/json",
					"Content-Md5":       "1B2M2Y8AsgTpgAmY7PhCfg==",
					"X-Amz-Meta-Sensor": "room-1",
				}),
			cr: creds,
			sr: SignRequest{Service: "s3", Region: "ap-northeast-1", PayloadHash: EmptyPayloadHash, Time: when},
			want: "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20260801/ap-northeast-1/s3/aws4_request, " +
				"SignedHeaders=content-md5;content-type;host;x-amz-content-sha256;x-amz-date;x-amz-meta-sensor, " +
				"Signature=e8efaf03977205554378d8dfe565af6f60a76128362c2d6bdaa544840c6f94e8",
		},
		{
			name: "dynamodb post with session token",
			req: build(http.MethodPost, "dynamodb.us-east-1.amazonaws.com", "/", nil,
				map[string]string{
					"Content-Type": "application/x-amz-json-1.0",
					"X-Amz-Target": "DynamoDB_20120810.PutItem",
				}),
			cr: credsWithToken,
			sr: SignRequest{Service: "dynamodb", Region: "us-east-1",
				PayloadHash: "44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a", Time: when},
			want: "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20260801/us-east-1/dynamodb/aws4_request, " +
				"SignedHeaders=content-type;host;x-amz-content-sha256;x-amz-date;x-amz-security-token;x-amz-target, " +
				"Signature=0afa180191feb8919d30efb31db8d97090fee858b90f8b72b8ccf82c5c527972",
		},
		{
			name: "s3 get with range and untrimmed header value",
			req: build(http.MethodGet, "bench-bucket.s3.ap-northeast-1.amazonaws.com",
				"/a/b~c.txt", nil,
				map[string]string{"Range": "bytes=0-1023", "X-Amz-Meta-Note": "  padded  "}),
			cr: creds,
			sr: SignRequest{Service: "s3", Region: "ap-northeast-1", PayloadHash: EmptyPayloadHash, Time: when},
			want: "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20260801/ap-northeast-1/s3/aws4_request, " +
				"SignedHeaders=host;range;x-amz-content-sha256;x-amz-date;x-amz-meta-note, " +
				"Signature=ae54ccca37c3a2b3ebf32686d77b53d14fd5366a0f5aa16968a3db78a95a7009",
		},
		{
			name: "double-encoded path service",
			req: build(http.MethodGet, "example.execute-api.us-west-2.amazonaws.com",
				"/stage/items name", nil, nil),
			cr: creds,
			sr: SignRequest{Service: "execute-api", Region: "us-west-2",
				PayloadHash: EmptyPayloadHash, DoubleEncodePath: true, Time: when},
			want: "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20260801/us-west-2/execute-api/aws4_request, " +
				"SignedHeaders=host;x-amz-content-sha256;x-amz-date, " +
				"Signature=e9d1e1c9ebc1edba2dc9026dc2855b122de92aa24bf2760e85316f835f5bf938",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			Sign(tt.req, tt.cr, tt.sr)
			got := tt.req.Header.Get("Authorization")
			if got != tt.want {
				t.Errorf("Authorization drifted:\n got %s\nwant %s", got, tt.want)
			}
			if !strings.Contains(got, "Signature=") {
				t.Fatal("no signature at all")
			}
		})
	}
}
