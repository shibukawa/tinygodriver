//go:build !tinygo

package s3_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shibukawa/tinygodriver/storage/s3"
)

// capture records what the fake endpoint received.
type capture struct {
	Method      string
	RequestURI  string
	Host        string
	Authz       string
	ContentSHA  string
	ContentType string
	Range       string
	Meta        string
	Body        []byte
}

// newServer starts a fake S3 endpoint. handler writes the reply; the request is
// recorded first so tests can assert on how it was built and signed.
func newServer(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) (*httptest.Server, *[]capture) {
	t.Helper()
	var seen []capture
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		seen = append(seen, capture{
			Method:      r.Method,
			RequestURI:  r.RequestURI,
			Host:        r.Host,
			Authz:       r.Header.Get("Authorization"),
			ContentSHA:  r.Header.Get("X-Amz-Content-Sha256"),
			ContentType: r.Header.Get("Content-Type"),
			Range:       r.Header.Get("Range"),
			Meta:        r.Header.Get("X-Amz-Meta-Owner"),
			Body:        body,
		})
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

func newClient(t *testing.T, endpoint string, opts ...s3.Option) *s3.Client {
	t.Helper()
	options := append([]s3.Option{
		s3.WithEndpoint(endpoint),
		s3.WithRegion("us-east-1"),
		s3.WithCredentials(s3.Credentials{AccessKeyID: "id", SecretAccessKey: "secret"}),
	}, opts...)
	client, err := s3.New(options...)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestPutSignsAndSendsBody(t *testing.T) {
	payload := bytes.Repeat([]byte("x"), 4096)
	srv, seen := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"abc"`)
	})
	client := newClient(t, srv.URL)

	res, err := client.Put(context.Background(), "bucket", "photos/2019 (a)/こん.txt",
		bytes.NewReader(payload),
		s3.WithContentType("text/plain"),
		s3.WithMetadata(map[string]string{"owner": "shibukawa"}))
	if err != nil {
		t.Fatal(err)
	}
	if res.ETag != `"abc"` {
		t.Errorf("ETag = %q", res.ETag)
	}

	got := (*seen)[0]
	if got.Method != http.MethodPut {
		t.Errorf("method = %s", got.Method)
	}
	// The request line must carry the AWS-style escaping the signature covers.
	const wantURI = "/bucket/photos/2019%20%28a%29/%E3%81%93%E3%82%93.txt"
	if got.RequestURI != wantURI {
		t.Errorf("request URI = %q, want %q", got.RequestURI, wantURI)
	}
	if !bytes.Equal(got.Body, payload) {
		t.Errorf("body = %d bytes, want %d", len(got.Body), len(payload))
	}
	if got.ContentSHA != sha256HexOf(payload) {
		t.Errorf("x-amz-content-sha256 = %q, does not match the body", got.ContentSHA)
	}
	if got.ContentType != "text/plain" {
		t.Errorf("Content-Type = %q", got.ContentType)
	}
	if got.Meta != "shibukawa" {
		t.Errorf("x-amz-meta-owner = %q", got.Meta)
	}
	for _, want := range []string{
		"AWS4-HMAC-SHA256 Credential=id/",
		"/us-east-1/s3/aws4_request",
		"SignedHeaders=content-type;host;x-amz-content-sha256;x-amz-date;x-amz-meta-owner",
	} {
		if !strings.Contains(got.Authz, want) {
			t.Errorf("Authorization %q missing %q", got.Authz, want)
		}
	}
}

func TestOperationTimeoutIncludesBodyWithCustomHTTPClient(t *testing.T) {
	srv, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		time.Sleep(200 * time.Millisecond)
		fmt.Fprint(w, "late")
	})
	client := newClient(t, srv.URL,
		s3.WithHTTPClient(srv.Client()),
		s3.WithTimeout(20*time.Millisecond))

	obj, err := client.Get(context.Background(), "bucket", "key")
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return
		}
		t.Fatal(err)
	}
	defer obj.Body.Close()
	if _, err := io.ReadAll(obj.Body); err == nil {
		t.Fatal("expected operation timeout while reading body")
	}
}

// TestPutStreamHashesUnseekableBody covers the buffering path: an io.Reader
// that cannot rewind still gets a body hash rather than UNSIGNED-PAYLOAD.
func TestPutStreamHashesUnseekableBody(t *testing.T) {
	payload := []byte("streamed payload")
	srv, seen := newServer(t, func(w http.ResponseWriter, r *http.Request) {})
	client := newClient(t, srv.URL)

	if _, err := client.Put(context.Background(), "bucket", "k",
		struct{ io.Reader }{bytes.NewReader(payload)}); err != nil {
		t.Fatal(err)
	}
	got := (*seen)[0]
	if !bytes.Equal(got.Body, payload) {
		t.Errorf("body = %q", got.Body)
	}
	if got.ContentSHA != sha256HexOf(payload) {
		t.Errorf("x-amz-content-sha256 = %q, want the body hash", got.ContentSHA)
	}
}

func TestPutUnsignedPayload(t *testing.T) {
	srv, seen := newServer(t, func(w http.ResponseWriter, r *http.Request) {})
	client := newClient(t, srv.URL, s3.WithUnsignedPayload(true))

	if _, err := client.Put(context.Background(), "bucket", "k",
		struct{ io.Reader }{strings.NewReader("body")}); err != nil {
		t.Fatal(err)
	}
	if got := (*seen)[0].ContentSHA; got != "UNSIGNED-PAYLOAD" {
		t.Errorf("x-amz-content-sha256 = %q, want UNSIGNED-PAYLOAD", got)
	}
}

// TestPutUnsignedStreamSendsLength guards the combination AWS rejects: an
// unsigned stream with no length goes out chunked, so WithContentLength has to
// put a Content-Length back on the request.
func TestPutUnsignedStreamSendsLength(t *testing.T) {
	var length int64
	var chunked bool
	srv, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		length = r.ContentLength
		chunked = len(r.TransferEncoding) > 0
	})
	client := newClient(t, srv.URL, s3.WithUnsignedPayload(true))
	body := struct{ io.Reader }{strings.NewReader("streamed body")}

	if _, err := client.Put(context.Background(), "bucket", "k", body,
		s3.WithContentLength(13)); err != nil {
		t.Fatal(err)
	}
	if chunked {
		t.Error("request used chunked transfer encoding")
	}
	if length != 13 {
		t.Errorf("Content-Length = %d, want 13", length)
	}
}

func TestGetAndRange(t *testing.T) {
	payload := []byte("0123456789abcdef")
	srv, seen := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"etag"`)
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Last-Modified", "Fri, 24 May 2013 00:00:00 GMT")
		w.Header().Set("X-Amz-Meta-Owner", "shibukawa")
		if r.Header.Get("Range") != "" {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes 4-7/%d", len(payload)))
			w.WriteHeader(http.StatusPartialContent)
			w.Write(payload[4:8])
			return
		}
		w.Write(payload)
	})
	client := newClient(t, srv.URL)

	obj, err := client.Get(context.Background(), "bucket", "k")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(obj.Body)
	obj.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(body, payload) {
		t.Errorf("body = %q, want %q", body, payload)
	}
	if obj.Size != int64(len(payload)) {
		t.Errorf("Size = %d, want %d", obj.Size, len(payload))
	}
	if obj.ETag != `"etag"` || obj.ContentType != "text/plain" {
		t.Errorf("metadata = %+v", obj.ObjectInfo)
	}
	if obj.Metadata["owner"] != "shibukawa" {
		t.Errorf("Metadata = %v", obj.Metadata)
	}
	if want := time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC); !obj.LastModified.Equal(want) {
		t.Errorf("LastModified = %v, want %v", obj.LastModified, want)
	}

	ranged, err := client.GetRange(context.Background(), "bucket", "k", 4, 4)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(ranged.Body)
	ranged.Body.Close()
	if string(body) != "4567" {
		t.Errorf("ranged body = %q, want %q", body, "4567")
	}
	// A ranged reply reports the size of the whole object, not of the slice.
	if ranged.Size != int64(len(payload)) {
		t.Errorf("ranged Size = %d, want %d", ranged.Size, len(payload))
	}
	if got := (*seen)[1].Range; got != "bytes=4-7" {
		t.Errorf("Range = %q, want bytes=4-7", got)
	}
}

func TestGetRangeToEnd(t *testing.T) {
	srv, seen := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPartialContent)
	})
	client := newClient(t, srv.URL)

	obj, err := client.GetRange(context.Background(), "bucket", "k", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	obj.Body.Close()
	if got := (*seen)[0].Range; got != "bytes=10-" {
		t.Errorf("Range = %q, want bytes=10-", got)
	}
}

func TestList(t *testing.T) {
	const doc = `<?xml version="1.0" encoding="UTF-8"?>
<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
  <IsTruncated>true</IsTruncated>
  <NextContinuationToken>token-2</NextContinuationToken>
  <Contents>
    <Key>photos/a.jpg</Key>
    <LastModified>2009-10-12T17:50:30.000Z</LastModified>
    <ETag>&quot;etag-a&quot;</ETag>
    <Size>434234</Size>
  </Contents>
  <CommonPrefixes><Prefix>photos/2019/</Prefix></CommonPrefixes>
</ListBucketResult>`

	srv, seen := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		io.WriteString(w, doc)
	})
	client := newClient(t, srv.URL)

	res, err := client.List(context.Background(), "bucket",
		s3.WithPrefix("photos/"), s3.WithDelimiter("/"), s3.WithMaxKeys(10),
		s3.WithContinuationToken("token-1"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Objects) != 1 || res.Objects[0].Key != "photos/a.jpg" || res.Objects[0].Size != 434234 {
		t.Errorf("Objects = %+v", res.Objects)
	}
	if res.Objects[0].LastModified.Year() != 2009 {
		t.Errorf("LastModified = %v", res.Objects[0].LastModified)
	}
	if len(res.CommonPrefixes) != 1 || res.CommonPrefixes[0] != "photos/2019/" {
		t.Errorf("CommonPrefixes = %v", res.CommonPrefixes)
	}
	if !res.IsTruncated || res.NextToken != "token-2" {
		t.Errorf("truncated = %v, token = %q", res.IsTruncated, res.NextToken)
	}

	const wantURI = "/bucket?continuation-token=token-1&delimiter=%2F&list-type=2&max-keys=10&prefix=photos%2F"
	if got := (*seen)[0].RequestURI; got != wantURI {
		t.Errorf("request URI = %q, want %q", got, wantURI)
	}
}

func TestHeadAndDelete(t *testing.T) {
	srv, seen := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Length", "73728")
		w.Header().Set("ETag", `"etag"`)
	})
	client := newClient(t, srv.URL)

	info, err := client.Head(context.Background(), "bucket", "k")
	if err != nil {
		t.Fatal(err)
	}
	if info.Size != 73728 || info.ETag != `"etag"` {
		t.Errorf("info = %+v", info)
	}
	if err := client.Delete(context.Background(), "bucket", "k"); err != nil {
		t.Fatal(err)
	}
	if (*seen)[1].Method != http.MethodDelete {
		t.Errorf("method = %s", (*seen)[1].Method)
	}
}

func TestErrorMapping(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
		want   error
		code   string
	}{
		{
			name: "no such key", status: http.StatusNotFound,
			body: `<Error><Code>NoSuchKey</Code><Message>The specified key does not exist.</Message><RequestId>REQ1</RequestId></Error>`,
			want: s3.ErrNoSuchKey, code: "NoSuchKey",
		},
		{
			name: "access denied", status: http.StatusForbidden,
			body: `<Error><Code>AccessDenied</Code><Message>Access Denied</Message></Error>`,
			want: s3.ErrAccessDenied, code: "AccessDenied",
		},
		{
			name: "bad signature", status: http.StatusForbidden,
			body: `<Error><Code>SignatureDoesNotMatch</Code></Error>`,
			want: s3.ErrBadCredentials, code: "SignatureDoesNotMatch",
		},
		{
			name: "status only", status: http.StatusNotFound, body: "",
			want: s3.ErrNoSuchKey,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			srv, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(test.status)
				io.WriteString(w, test.body)
			})
			client := newClient(t, srv.URL)

			_, err := client.Get(context.Background(), "bucket", "k")
			if !errors.Is(err, test.want) {
				t.Fatalf("err = %v, want %v", err, test.want)
			}
			var s3err *s3.Error
			if !errors.As(err, &s3err) {
				t.Fatalf("err is not *s3.Error: %v", err)
			}
			if s3err.StatusCode != test.status || s3err.Code != test.code {
				t.Errorf("Error = %+v", s3err)
			}
			if s3err.Bucket != "bucket" || s3err.Key != "k" || s3err.Op != "Get" {
				t.Errorf("Error target = %+v", s3err)
			}
		})
	}
}

// TestRedirectIsResigned is the behaviour TinyGo forces and standard Go has to
// be told about: a redirect must be re-signed for its new host, so http.Client
// must not follow it silently.
func TestRedirectIsResigned(t *testing.T) {
	final, finalSeen := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"moved"`)
	})
	first, firstSeen := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", final.URL+r.URL.Path)
		w.Header().Set("X-Amz-Bucket-Region", "eu-west-1")
		w.WriteHeader(http.StatusTemporaryRedirect)
	})
	client := newClient(t, first.URL)

	res, err := client.Put(context.Background(), "bucket", "k", strings.NewReader("payload"))
	if err != nil {
		t.Fatal(err)
	}
	if res.ETag != `"moved"` {
		t.Errorf("ETag = %q", res.ETag)
	}
	if len(*finalSeen) != 1 {
		t.Fatalf("redirect target saw %d requests", len(*finalSeen))
	}

	before, after := (*firstSeen)[0], (*finalSeen)[0]
	if after.Authz == before.Authz {
		t.Error("redirected request reused the original signature")
	}
	if !strings.Contains(after.Authz, "/eu-west-1/s3/aws4_request") {
		t.Errorf("redirect not signed for the new region: %s", after.Authz)
	}
	if string(after.Body) != "payload" {
		t.Errorf("redirected body = %q, want the original payload", after.Body)
	}
	if client.Region() != "eu-west-1" {
		t.Errorf("Region = %q, want the region the redirect reported", client.Region())
	}
}

func TestRedirectLoopStops(t *testing.T) {
	var srv *httptest.Server
	srv, _ = newServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", srv.URL+"/loop")
		w.WriteHeader(http.StatusTemporaryRedirect)
	})
	client := newClient(t, srv.URL)

	_, err := client.Get(context.Background(), "bucket", "k")
	if !errors.Is(err, s3.ErrTooManyRedirect) {
		t.Fatalf("err = %v, want ErrTooManyRedirect", err)
	}
}

func TestNewRequiresCredentialsAndRegion(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "")
	t.Setenv("AWS_REGION", "")
	t.Setenv("AWS_DEFAULT_REGION", "")

	if _, err := s3.New(s3.WithRegion("us-east-1")); !errors.Is(err, s3.ErrNoCredentials) {
		t.Errorf("err = %v, want ErrNoCredentials", err)
	}
	creds := s3.Credentials{AccessKeyID: "id", SecretAccessKey: "secret"}
	if _, err := s3.New(s3.WithCredentials(creds)); !errors.Is(err, s3.ErrNoRegion) {
		t.Errorf("err = %v, want ErrNoRegion", err)
	}
}

func TestNewReadsEnvironment(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "env-id")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "env-secret")
	t.Setenv("AWS_REGION", "ap-northeast-1")
	t.Setenv("AWS_ENDPOINT_URL_S3", "http://127.0.0.1:19000")

	client, err := s3.New()
	if err != nil {
		t.Fatal(err)
	}
	if client.Region() != "ap-northeast-1" {
		t.Errorf("Region = %q", client.Region())
	}
	if client.Endpoint() != "http://127.0.0.1:19000" {
		t.Errorf("Endpoint = %q", client.Endpoint())
	}
}

func TestCreateBucketSendsLocation(t *testing.T) {
	srv, seen := newServer(t, func(w http.ResponseWriter, r *http.Request) {})

	client := newClient(t, srv.URL, s3.WithRegion("ap-northeast-1"))
	if err := client.CreateBucket(context.Background(), "bucket"); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains((*seen)[0].Body, []byte("<LocationConstraint>ap-northeast-1</LocationConstraint>")) {
		t.Errorf("body = %q", (*seen)[0].Body)
	}

	// us-east-1 is the default location and must not be stated.
	client = newClient(t, srv.URL)
	if err := client.CreateBucket(context.Background(), "bucket"); err != nil {
		t.Fatal(err)
	}
	if len((*seen)[1].Body) != 0 {
		t.Errorf("us-east-1 body = %q, want empty", (*seen)[1].Body)
	}
}

func sha256HexOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
