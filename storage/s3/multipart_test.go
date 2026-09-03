//go:build !tinygo

package s3_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/shibukawa/tinygodriver/storage/s3"
)

// TestMultipartUploadRequests walks the four calls against a fake endpoint and
// checks each request line, since the subresource query is what tells S3
// which operation a POST or PUT on the key is.
func TestMultipartUploadRequests(t *testing.T) {
	srv, seen := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		// The subresource keeps its "=", see canonicalQuery.
		case r.Method == http.MethodPost && r.URL.RawQuery == "uploads=":
			w.Header().Set("Content-Type", "application/xml")
			io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?>
<InitiateMultipartUploadResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
  <Bucket>bucket</Bucket><Key>big/こん.bin</Key><UploadId>up&amp;1</UploadId>
</InitiateMultipartUploadResult>`)
		case r.Method == http.MethodPut:
			w.Header().Set("ETag", `"part-`+r.URL.Query().Get("partNumber")+`"`)
		case r.Method == http.MethodPost:
			w.Header().Set("X-Amz-Version-Id", "v1")
			io.WriteString(w, `<CompleteMultipartUploadResult>
  <Location>http://x/bucket/big</Location><Bucket>bucket</Bucket><Key>big</Key>
  <ETag>"final-2"</ETag>
</CompleteMultipartUploadResult>`)
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		}
	})
	client := newClient(t, srv.URL)
	ctx := context.Background()

	upload, err := client.CreateMultipartUpload(ctx, "bucket", "big/こん.bin",
		s3.WithContentType("application/octet-stream"),
		s3.WithMetadata(map[string]string{"owner": "shibukawa"}))
	if err != nil {
		t.Fatal(err)
	}
	if upload.UploadID != "up&1" || upload.Bucket != "bucket" || upload.Key != "big/こん.bin" {
		t.Errorf("upload = %+v", upload)
	}

	one, err := client.UploadPart(ctx, *upload, 1, bytes.NewReader(bytes.Repeat([]byte("a"), 64)))
	if err != nil {
		t.Fatal(err)
	}
	two, err := client.UploadPart(ctx, *upload, 2, struct{ io.Reader }{strings.NewReader("tail")})
	if err != nil {
		t.Fatal(err)
	}
	if one.ETag != `"part-1"` || one.PartNumber != 1 || two.ETag != `"part-2"` {
		t.Errorf("parts = %+v %+v", one, two)
	}

	res, err := client.CompleteMultipartUpload(ctx, *upload, []s3.CompletedPart{*one, *two})
	if err != nil {
		t.Fatal(err)
	}
	if res.ETag != `"final-2"` || res.VersionID != "v1" {
		t.Errorf("result = %+v", res)
	}
	if err := client.AbortMultipartUpload(ctx, *upload); err != nil {
		t.Fatal(err)
	}

	const key = "/bucket/big/%E3%81%93%E3%82%93.bin"
	want := []struct{ method, uri string }{
		{http.MethodPost, key + "?uploads="},
		{http.MethodPut, key + "?partNumber=1&uploadId=up%261"},
		{http.MethodPut, key + "?partNumber=2&uploadId=up%261"},
		{http.MethodPost, key + "?uploadId=up%261"},
		{http.MethodDelete, key + "?uploadId=up%261"},
	}
	if len(*seen) != len(want) {
		t.Fatalf("saw %d requests, want %d", len(*seen), len(want))
	}
	for i, w := range want {
		got := (*seen)[i]
		if got.Method != w.method || got.RequestURI != w.uri {
			t.Errorf("request %d = %s %s, want %s %s", i, got.Method, got.RequestURI, w.method, w.uri)
		}
	}
	create := (*seen)[0]
	if create.ContentType != "application/octet-stream" || create.Meta != "shibukawa" {
		t.Errorf("create carried Content-Type %q, x-amz-meta-owner %q", create.ContentType, create.Meta)
	}
	if got := (*seen)[1]; got.ContentSHA != sha256HexOf(bytes.Repeat([]byte("a"), 64)) || len(got.Body) != 64 {
		t.Errorf("part 1 hash %q over %d bytes", got.ContentSHA, len(got.Body))
	}
	// Quotes need no escaping in element text, so the ETags go as they came.
	const wantDoc = `<CompleteMultipartUpload xmlns="http://s3.amazonaws.com/doc/2006-03-01/">` +
		`<Part><PartNumber>1</PartNumber><ETag>"part-1"</ETag></Part>` +
		`<Part><PartNumber>2</PartNumber><ETag>"part-2"</ETag></Part>` +
		`</CompleteMultipartUpload>`
	if got := string((*seen)[3].Body); got != wantDoc {
		t.Errorf("complete body =\n%s\nwant\n%s", got, wantDoc)
	}
	if got := (*seen)[3].ContentType; got != "application/xml" {
		t.Errorf("complete Content-Type = %q", got)
	}
}

// S3 answers CompleteMultipartUpload with 200 and an error document when the
// assembly fails after the headers went out. It has to surface as an error.
func TestCompleteMultipartUploadErrorInside200(t *testing.T) {
	srv, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `<Error><Code>InvalidPart</Code><Message>One or more of the specified parts could not be found.</Message></Error>`)
	})
	client := newClient(t, srv.URL)
	upload := s3.MultipartUpload{Bucket: "bucket", Key: "k", UploadID: "id"}

	_, err := client.CompleteMultipartUpload(context.Background(), upload, []s3.CompletedPart{{PartNumber: 1, ETag: `"x"`}})
	if !errors.Is(err, s3.ErrInvalidPart) {
		t.Fatalf("err = %v, want ErrInvalidPart", err)
	}
	var s3err *s3.Error
	if !errors.As(err, &s3err) || s3err.StatusCode != http.StatusOK || s3err.Op != "CompleteMultipartUpload" {
		t.Errorf("err = %+v", err)
	}
}

func TestMultipartUploadErrors(t *testing.T) {
	srv, seen := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, `<Error><Code>NoSuchUpload</Code></Error>`)
	})
	client := newClient(t, srv.URL)
	upload := s3.MultipartUpload{Bucket: "bucket", Key: "k", UploadID: "gone"}

	if err := client.AbortMultipartUpload(context.Background(), upload); !errors.Is(err, s3.ErrNoSuchUpload) {
		t.Errorf("abort err = %v, want ErrNoSuchUpload", err)
	}
	// A part number outside the range never reaches the wire.
	if _, err := client.UploadPart(context.Background(), upload, 10001, strings.NewReader("x")); !errors.Is(err, s3.ErrInvalidPart) {
		t.Errorf("part 10001 err = %v, want ErrInvalidPart", err)
	}
	if _, err := client.UploadPart(context.Background(), upload, 0, strings.NewReader("x")); !errors.Is(err, s3.ErrInvalidPart) {
		t.Errorf("part 0 err = %v, want ErrInvalidPart", err)
	}
	if len(*seen) != 1 {
		t.Errorf("saw %d requests, want only the abort", len(*seen))
	}
}
