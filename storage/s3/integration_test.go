//go:build !tinygo

package s3_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

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
