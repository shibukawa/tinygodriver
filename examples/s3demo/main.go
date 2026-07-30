// Command s3demo exercises the storage/s3 client against any S3-compatible
// endpoint: it uploads an object, reads it back whole and by range, lists the
// bucket, and deletes what it created.
//
// One source, both compilers:
//
//	go run ./examples/s3demo
//	tinygo build -o s3demo ./examples/s3demo && ./s3demo
//
// Configure it through the usual AWS environment variables, for example against
// a local RustFS:
//
//	AWS_ENDPOINT_URL_S3=http://127.0.0.1:19000 \
//	AWS_ACCESS_KEY_ID=rustfsadmin AWS_SECRET_ACCESS_KEY=rustfsadmin \
//	AWS_REGION=us-east-1 ./s3demo
package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	_ "github.com/shibukawa/tinygodriver/netdev"
	"github.com/shibukawa/tinygodriver/storage/s3"
)

func main() {
	if err := run(); err != nil {
		fmt.Println("error:", err)
		os.Exit(1)
	}
}

func run() error {
	bucket := env("S3_BUCKET", "tinygodriver-demo")
	key := "demo/" + time.Now().UTC().Format("20060102T150405Z") + ".txt"
	payload := []byte("hello from " + s3.Backend + "\n")

	client, err := s3.New()
	if err != nil {
		return err
	}
	fmt.Printf("backend=%s endpoint=%s region=%s\n\n", s3.Backend, client.Endpoint(), client.Region())

	ctx := context.Background()
	if err := client.CreateBucket(ctx, bucket); err != nil && !errors.Is(err, s3.ErrBucketExists) {
		return err
	}

	put, err := client.Put(ctx, bucket, key, bytes.NewReader(payload),
		s3.WithContentType("text/plain; charset=utf-8"))
	if err != nil {
		return err
	}
	fmt.Printf("put    %s (%d bytes) etag=%s\n", key, len(payload), put.ETag)

	obj, err := client.Get(ctx, bucket, key)
	if err != nil {
		return err
	}
	body, err := io.ReadAll(obj.Body)
	obj.Body.Close()
	if err != nil {
		return err
	}
	fmt.Printf("get    %d bytes, content-type=%s: %s", len(body), obj.ContentType, body)

	ranged, err := client.GetRange(ctx, bucket, key, 0, 5)
	if err != nil {
		return err
	}
	head, err := io.ReadAll(ranged.Body)
	ranged.Body.Close()
	if err != nil {
		return err
	}
	fmt.Printf("range  first 5 of %d bytes: %q\n", ranged.Size, head)

	list, err := client.List(ctx, bucket, s3.WithPrefix("demo/"), s3.WithMaxKeys(5))
	if err != nil {
		return err
	}
	fmt.Printf("list   %d object(s) under demo/\n", len(list.Objects))
	for _, o := range list.Objects {
		fmt.Printf("       %s (%d bytes, %s)\n", o.Key, o.Size, o.LastModified.Format(time.RFC3339))
	}

	if err := client.Delete(ctx, bucket, key); err != nil {
		return err
	}
	fmt.Printf("delete %s\n", key)

	if _, err := client.Head(ctx, bucket, key); !errors.Is(err, s3.ErrNoSuchKey) {
		return fmt.Errorf("deleted object still present: %v", err)
	}
	fmt.Println("head   gone, as expected")
	return nil
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
