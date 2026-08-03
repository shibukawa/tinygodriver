package jwt_test

import (
	"fmt"

	"github.com/shibukawa/tinygodriver/jwt"
)

func ExampleSign() {
	signer, err := jwt.NewHMACSigner([]byte("01234567890123456789012345678901"))
	if err != nil {
		panic(err)
	}
	raw, err := jwt.Sign(jwt.Header{}, jwt.Claims{Issuer: "issuer"}, signer)
	if err != nil {
		panic(err)
	}
	token, err := jwt.Parse(raw, jwt.ParseOptions{})
	if err != nil {
		panic(err)
	}
	fmt.Println(token.Header.Algorithm)
	// Output: HS256
}
