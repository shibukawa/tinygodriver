//go:build !tinygo

package aws

import (
	"net/http"
	"net/url"
	"testing"
	"time"
)

// The benchmark request is the shape a PutObject takes on the wire: a real
// path, a couple of standard headers, and the x-amz headers Sign itself adds.
// Signing is on the path of every call every client in this repository makes,
// so its cost is worth a number.

var benchCreds = Credentials{
	AccessKeyID:     "AKIDEXAMPLE",
	SecretAccessKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY",
}

var benchSignTime = time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

func newBenchRequest() *http.Request {
	target := &url.URL{
		Scheme:   "https",
		Host:     "bench-bucket.s3.ap-northeast-1.amazonaws.com",
		Path:     "/sensor readings/2026/08/01.json",
		RawPath:  URIEncode("/sensor readings/2026/08/01.json", false),
		RawQuery: CanonicalQuery([][2]string{{"partNumber", "1"}, {"uploadId", "abc123"}}),
	}
	req := &http.Request{
		Method: http.MethodPut,
		URL:    target,
		Host:   target.Host,
		Header: http.Header{},
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Md5", "1B2M2Y8AsgTpgAmY7PhCfg==")
	req.Header.Set("X-Amz-Meta-Sensor", "room-1")
	return req
}

func BenchmarkSign(b *testing.B) {
	req := newBenchRequest()
	sr := SignRequest{
		Service:     "s3",
		Region:      "ap-northeast-1",
		PayloadHash: EmptyPayloadHash,
		Time:        benchSignTime,
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		Sign(req, benchCreds, sr)
	}
}
