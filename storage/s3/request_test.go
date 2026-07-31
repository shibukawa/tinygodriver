//go:build !tinygo

// These tests cover request construction: the property that what buildURL puts
// on the wire is what cloud/aws feeds into the canonical request. The signer's
// own tests live with the signer.
//
// They run under host Go. Without tags they exercise the standard-Go path; with
// -tags force_tinygo_logic they exercise the TinyGo path, so it is testable
// without a TinyGo toolchain.
package s3

import "testing"

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
