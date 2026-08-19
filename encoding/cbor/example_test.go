package cbor_test

import (
	"bytes"
	"fmt"

	"github.com/shibukawa/tinygodriver/encoding/cbor"
)

func ExampleNewEncoder() {
	var encoded bytes.Buffer
	encoder, err := cbor.NewEncoder(&encoded, cbor.EncoderOptions{})
	if err != nil {
		panic(err)
	}
	if err := encoder.WriteText("ok"); err != nil {
		panic(err)
	}
	fmt.Printf("%x\n", encoded.Bytes())
	// Output: 626f6b
}
