package zstd_test

import (
	"bytes"
	"fmt"
	"io"

	"github.com/shibukawa/tinygodriver/compress/zstd"
)

func ExampleEncodeAll() {
	encoded, result, err := zstd.EncodeAll([]byte("response body"))
	if err != nil {
		panic(err)
	}
	fmt.Println(len(encoded) == int(result.Size))
	fmt.Println(zstd.ContentEncoding)
	// Output:
	// true
	// zstd
}

func ExampleDecodeAll() {
	encoded, _, err := zstd.EncodeAll([]byte("request body"))
	if err != nil {
		panic(err)
	}
	// Bound what a few bytes of untrusted input may expand to.
	body, err := zstd.DecodeAll(nil, encoded, zstd.WithMaxOutput(1<<20))
	if err != nil {
		panic(err)
	}
	fmt.Println(string(body))
	// Output:
	// request body
}

func ExampleNewReader() {
	encoded, _, err := zstd.EncodeAll([]byte("streamed body"))
	if err != nil {
		panic(err)
	}
	r, err := zstd.NewReader(bytes.NewReader(encoded))
	if err != nil {
		panic(err)
	}
	defer r.Close()
	body, err := io.ReadAll(r)
	if err != nil {
		panic(err)
	}
	fmt.Println(string(body))
	// Output:
	// streamed body
}
