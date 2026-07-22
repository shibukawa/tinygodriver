package zstd_test

import (
	"fmt"

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
