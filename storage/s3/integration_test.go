//go:build !tinygo

package s3_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/shibukawa/tinygodriver/storage/s3"
)

// TestIntegration runs the whole surface against a real S3-compatible endpoint.
// It is skipped unless S3_TEST_ENDPOINT is set, for example against RustFS:
//
//	docker run -d -p 19000:9000 -e RUSTFS_ACCESS_KEY=rustfsadmin \
//		-e RUSTFS_SECRET_KEY=rustfsadmin -e RUSTFS_VOLUMES=/data rustfs/rustfs
//	S3_TEST_ENDPOINT=http://127.0.0.1:19000 \
//		S3_TEST_ACCESS_KEY=rustfsadmin S3_TEST_SECRET_KEY=rustfsadmin go test ./storage/s3/
func TestIntegration(t *testing.T) {
	endpoint := os.Getenv("S3_TEST_ENDPOINT")
	if endpoint == "" {
		t.Skip("set S3_TEST_ENDPOINT to run integration tests")
	}
	region := os.Getenv("S3_TEST_REGION")
	if region == "" {
		region = "us-east-1"
	}
	bucket := os.Getenv("S3_TEST_BUCKET")
	if bucket == "" {
		bucket = "tinygodriver-test"
	}

	client, err := s3.New(
		s3.WithEndpoint(endpoint),
		s3.WithRegion(region),
		s3.WithCredentials(s3.Credentials{
			AccessKeyID:     os.Getenv("S3_TEST_ACCESS_KEY"),
			SecretAccessKey: os.Getenv("S3_TEST_SECRET_KEY"),
			SessionToken:    os.Getenv("S3_TEST_SESSION_TOKEN"),
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	if err := client.CreateBucket(ctx, bucket); err != nil && !errors.Is(err, s3.ErrBucketExists) {
		t.Fatal(err)
	}

	// A key with a space, parentheses, and multi-byte characters is where
	// signing and escaping disagree if they are going to.
	const key = "photos/2019 (a)/こん.txt"
	payload := bytes.Repeat([]byte("tinygo-s3-payload."), 4096)

	put, err := client.Put(ctx, bucket, key, bytes.NewReader(payload),
		s3.WithContentType("text/plain; charset=utf-8"),
		s3.WithMetadata(map[string]string{"owner": "tinygodriver"}))
	if err != nil {
		t.Fatal(err)
	}
	if put.ETag == "" {
		t.Error("Put returned no ETag")
	}
	t.Cleanup(func() { client.Delete(context.Background(), bucket, key) })

	obj, err := client.Get(ctx, bucket, key)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(obj.Body)
	obj.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("Get returned %d bytes, want %d", len(got), len(payload))
	}
	if obj.ContentType != "text/plain; charset=utf-8" {
		t.Errorf("ContentType = %q", obj.ContentType)
	}
	if obj.Metadata["owner"] != "tinygodriver" {
		t.Errorf("Metadata = %v", obj.Metadata)
	}

	ranged, err := client.GetRange(ctx, bucket, key, 10, 10)
	if err != nil {
		t.Fatal(err)
	}
	got, _ = io.ReadAll(ranged.Body)
	ranged.Body.Close()
	if !bytes.Equal(got, payload[10:20]) {
		t.Errorf("GetRange = %q, want %q", got, payload[10:20])
	}
	if ranged.Size != int64(len(payload)) {
		t.Errorf("ranged Size = %d, want %d", ranged.Size, len(payload))
	}

	info, err := client.Head(ctx, bucket, key)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size != int64(len(payload)) {
		t.Errorf("Head Size = %d, want %d", info.Size, len(payload))
	}

	list, err := client.List(ctx, bucket, s3.WithPrefix("photos/"))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, o := range list.Objects {
		if o.Key == key {
			found = true
			if o.Size != int64(len(payload)) {
				t.Errorf("listed size = %d, want %d", o.Size, len(payload))
			}
		}
	}
	if !found {
		t.Errorf("List did not return %q: %+v", key, list.Objects)
	}

	// Streaming a body the package cannot rewind still has to arrive intact.
	const streamKey = "streamed.txt"
	if _, err := client.Put(ctx, bucket, streamKey,
		struct{ io.Reader }{strings.NewReader("streamed body")}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Delete(context.Background(), bucket, streamKey) })
	streamed, err := client.Get(ctx, bucket, streamKey)
	if err != nil {
		t.Fatal(err)
	}
	got, _ = io.ReadAll(streamed.Body)
	streamed.Body.Close()
	if string(got) != "streamed body" {
		t.Errorf("streamed object = %q", got)
	}

	// A presigned URL is only evidence once a client that holds no credentials
	// has used it: the PUT goes up through a plain http.Client with exactly the
	// signed headers, and the GET comes back down the same way.
	const presignKey = "presigned/2019 (b)/こん.bin"
	putURL, err := client.Presign(ctx, bucket, presignKey, s3.PresignOptions{
		Method: "PUT", Expires: time.Minute, ContentType: "application/octet-stream",
		Headers: map[string]string{"X-Amz-Meta-Owner": "presign"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Delete(context.Background(), bucket, presignKey) })
	putReq, _ := http.NewRequestWithContext(ctx, http.MethodPut, putURL.String(), bytes.NewReader(payload))
	putReq.Header.Set("Content-Type", "application/octet-stream")
	putReq.Header.Set("X-Amz-Meta-Owner", "presign")
	putResp, err := http.DefaultClient.Do(putReq)
	if err != nil {
		t.Fatal(err)
	}
	putBody, _ := io.ReadAll(putResp.Body)
	putResp.Body.Close()
	if putResp.StatusCode != http.StatusOK {
		t.Fatalf("presigned PUT = %d: %s", putResp.StatusCode, putBody)
	}
	info, err = client.Head(ctx, bucket, presignKey)
	if err != nil {
		t.Fatal(err)
	}
	if info.ContentType != "application/octet-stream" || info.Metadata["owner"] != "presign" {
		t.Errorf("presigned PUT stored %+v", info)
	}

	// The signed content-type is a constraint on the sender, not a hint.
	wrongType, _ := http.NewRequestWithContext(ctx, http.MethodPut, putURL.String(), bytes.NewReader(payload))
	wrongType.Header.Set("Content-Type", "text/plain")
	wrongType.Header.Set("X-Amz-Meta-Owner", "presign")
	if resp, err := http.DefaultClient.Do(wrongType); err != nil {
		t.Fatal(err)
	} else {
		drainAndClose(resp)
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("presigned PUT with a different Content-Type = %d, want 403", resp.StatusCode)
		}
	}

	getURL, err := client.Presign(ctx, bucket, presignKey, s3.PresignOptions{
		Expires: time.Minute,
		Query:   map[string]string{"response-content-disposition": `attachment; filename="down.bin"`},
	})
	if err != nil {
		t.Fatal(err)
	}
	getResp, err := http.Get(getURL.String())
	if err != nil {
		t.Fatal(err)
	}
	got, _ = io.ReadAll(getResp.Body)
	getResp.Body.Close()
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("presigned GET = %d: %s", getResp.StatusCode, got)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("presigned GET returned %d bytes, want %d", len(got), len(payload))
	}
	if cd := getResp.Header.Get("Content-Disposition"); cd != `attachment; filename="down.bin"` {
		t.Errorf("response-content-disposition was not applied: %q", cd)
	}

	// A URL whose signed parameters were edited must be refused, which is
	// what shows the endpoint verified the signature rather than the shape.
	tampered := strings.Replace(getURL.String(), "X-Amz-Expires=60", "X-Amz-Expires=600", 1)
	if resp, err := http.Get(tampered); err != nil {
		t.Fatal(err)
	} else {
		drainAndClose(resp)
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("tampered presigned GET = %d, want 403", resp.StatusCode)
		}
	}

	// Multipart: two parts through the client, a third through a presigned
	// part URL, assembled and read back whole. The first two are at the 5 MiB
	// minimum the endpoint enforces on every part but the last.
	const partSize = 5 << 20
	const multiKey = "multipart/2019 (c)/こん.bin"
	upload, err := client.CreateMultipartUpload(ctx, bucket, multiKey,
		s3.WithContentType("application/octet-stream"),
		s3.WithMetadata(map[string]string{"owner": "multipart"}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		client.AbortMultipartUpload(context.Background(), *upload)
		client.Delete(context.Background(), bucket, multiKey)
	})
	partA := bytes.Repeat([]byte("A"), partSize)
	partB := bytes.Repeat([]byte("B"), partSize)
	partC := []byte("C-tail")
	one, err := client.UploadPart(ctx, *upload, 1, bytes.NewReader(partA))
	if err != nil {
		t.Fatal(err)
	}
	two, err := client.UploadPart(ctx, *upload, 2, struct{ io.Reader }{bytes.NewReader(partB)})
	if err != nil {
		t.Fatal(err)
	}
	partURL, err := client.Presign(ctx, bucket, multiKey, s3.PresignOptions{
		Method: "PUT", Expires: time.Minute,
		Query: map[string]string{"uploadId": upload.UploadID, "partNumber": "3"},
	})
	if err != nil {
		t.Fatal(err)
	}
	partReq, _ := http.NewRequestWithContext(ctx, http.MethodPut, partURL.String(), bytes.NewReader(partC))
	partResp, err := http.DefaultClient.Do(partReq)
	if err != nil {
		t.Fatal(err)
	}
	partBody, _ := io.ReadAll(partResp.Body)
	partResp.Body.Close()
	if partResp.StatusCode != http.StatusOK {
		t.Fatalf("presigned UploadPart = %d: %s", partResp.StatusCode, partBody)
	}
	three := s3.CompletedPart{PartNumber: 3, ETag: partResp.Header.Get("ETag")}
	if one.ETag == "" || two.ETag == "" || three.ETag == "" {
		t.Fatalf("parts without ETags: %+v %+v %+v", one, two, three)
	}
	completed, err := client.CompleteMultipartUpload(ctx, *upload, []s3.CompletedPart{*one, *two, three})
	if err != nil {
		t.Fatal(err)
	}
	if completed.ETag == "" {
		t.Error("CompleteMultipartUpload returned no ETag")
	}
	whole, err := client.Get(ctx, bucket, multiKey)
	if err != nil {
		t.Fatal(err)
	}
	got, _ = io.ReadAll(whole.Body)
	whole.Body.Close()
	if !bytes.Equal(got, append(append(append([]byte{}, partA...), partB...), partC...)) {
		t.Errorf("assembled object is %d bytes, want %d", len(got), 2*partSize+len(partC))
	}
	if whole.ContentType != "application/octet-stream" || whole.Metadata["owner"] != "multipart" {
		t.Errorf("assembled object metadata = %+v", whole.ObjectInfo)
	}

	// An abandoned upload aborts, and a second abort reports it gone.
	orphan, err := client.CreateMultipartUpload(ctx, bucket, "multipart/orphan")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.UploadPart(ctx, *orphan, 1, strings.NewReader("x")); err != nil {
		t.Fatal(err)
	}
	if err := client.AbortMultipartUpload(ctx, *orphan); err != nil {
		t.Fatal(err)
	}
	if err := client.AbortMultipartUpload(ctx, *orphan); !errors.Is(err, s3.ErrNoSuchUpload) {
		t.Errorf("second abort = %v, want ErrNoSuchUpload", err)
	}

	if _, err := client.Get(ctx, bucket, "definitely-missing"); !errors.Is(err, s3.ErrNoSuchKey) {
		t.Errorf("missing key error = %v, want ErrNoSuchKey", err)
	}

	if err := client.Delete(ctx, bucket, key); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Head(ctx, bucket, key); !errors.Is(err, s3.ErrNoSuchKey) {
		t.Errorf("Head after Delete = %v, want ErrNoSuchKey", err)
	}
}

func drainAndClose(resp *http.Response) {
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
}
