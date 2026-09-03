//go:build !tinygo

package s3

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// The presign tests pin whole URLs under a fixed clock: the pieces Presign
// draws from the client (endpoint, addressing, region, credentials) and the
// pieces it takes from the options both have to reach the wire unchanged.
func TestPresignPinnedURLs(t *testing.T) {
	when := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	const key = "photos/2019 (a)/こん.txt"
	const credential = "X-Amz-Credential=id%2F20260801%2Fus-east-1%2Fs3%2Faws4_request"

	for _, test := range []struct {
		name     string
		endpoint string
		options  []Option
		opts     PresignOptions
		want     string
	}{
		{
			name: "aws get virtual host", endpoint: "https://s3.us-east-1.amazonaws.com",
			want: "https://bucket.s3.us-east-1.amazonaws.com/photos/2019%20%28a%29/%E3%81%93%E3%82%93.txt" +
				"?X-Amz-Algorithm=AWS4-HMAC-SHA256&" + credential +
				"&X-Amz-Date=20260801T120000Z&X-Amz-Expires=900&X-Amz-SignedHeaders=host" +
				"&X-Amz-Signature=d0d9bdd3ac1c4ad4497342978874aacd9ff2c9b99ce2010f28ee2126e54a8aba",
		},
		{
			name: "compatible endpoint put with content type and metadata", endpoint: "http://127.0.0.1:9000",
			opts: PresignOptions{
				Method: "PUT", Expires: time.Hour, ContentType: "text/plain",
				Headers: map[string]string{"X-Amz-Meta-Owner": "shibukawa"},
			},
			want: "http://127.0.0.1:9000/bucket/photos/2019%20%28a%29/%E3%81%93%E3%82%93.txt" +
				"?X-Amz-Algorithm=AWS4-HMAC-SHA256&" + credential +
				"&X-Amz-Date=20260801T120000Z&X-Amz-Expires=3600" +
				"&X-Amz-SignedHeaders=content-type%3Bhost%3Bx-amz-meta-owner" +
				"&X-Amz-Signature=9d59262067b333e16df994f4af02d23d5f47861b2e0dc37f3e78b949345a13a6",
		},
		{
			name: "path style forced on aws with a response query", endpoint: "https://s3.us-east-1.amazonaws.com",
			options: []Option{WithPathStyle(true)},
			opts: PresignOptions{
				Query: map[string]string{"response-content-disposition": "attachment"},
			},
			want: "https://s3.us-east-1.amazonaws.com/bucket/photos/2019%20%28a%29/%E3%81%93%E3%82%93.txt" +
				"?X-Amz-Algorithm=AWS4-HMAC-SHA256&" + credential +
				"&X-Amz-Date=20260801T120000Z&X-Amz-Expires=900&X-Amz-SignedHeaders=host" +
				"&response-content-disposition=attachment" +
				"&X-Amz-Signature=cc868dacf80ad71c3f79414b978ff6736ec01ec475c3937ca3e56c9eb563a5c3",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := newTestClient(t, test.endpoint, test.options...)
			client.now = func() time.Time { return when }
			u, err := client.Presign(nil, "bucket", key, test.opts)
			if err != nil {
				t.Fatal(err)
			}
			if got := u.String(); got != test.want {
				t.Errorf("URL drifted:\n got %s\nwant %s", got, test.want)
			}
		})
	}
}

func TestPresignExpiryRange(t *testing.T) {
	client := newTestClient(t, "http://127.0.0.1:9000")
	for _, expires := range []time.Duration{-time.Second, MaxPresignExpiry + time.Second} {
		if _, err := client.Presign(nil, "b", "k", PresignOptions{Expires: expires}); !errors.Is(err, ErrPresignExpiry) {
			t.Errorf("Expires=%v: err = %v, want ErrPresignExpiry", expires, err)
		}
	}
	// The cap itself is allowed, and a fraction of a second rounds up rather
	// than down to a zero S3 rejects.
	u, err := client.Presign(nil, "b", "k", PresignOptions{Expires: MaxPresignExpiry})
	if err != nil || !strings.Contains(u.RawQuery, "X-Amz-Expires=604800&") {
		t.Errorf("Expires=max: %v, %v", u, err)
	}
	u, err = client.Presign(nil, "b", "k", PresignOptions{Expires: 10 * time.Millisecond})
	if err != nil || !strings.Contains(u.RawQuery, "X-Amz-Expires=1&") {
		t.Errorf("Expires=10ms: %v, %v", u, err)
	}
}

// A presigned URL follows the region a redirect taught the client, as every
// other request does.
func TestPresignUsesCurrentRegion(t *testing.T) {
	client := newTestClient(t, "https://s3.us-east-1.amazonaws.com")
	client.setRegion("eu-west-1")
	u, err := client.Presign(nil, "b", "k", PresignOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(u.RawQuery, "%2Feu-west-1%2Fs3%2Faws4_request") {
		t.Errorf("credential scope did not follow the region: %s", u.RawQuery)
	}
}
