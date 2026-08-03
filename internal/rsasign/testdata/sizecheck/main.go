package main

import (
	"crypto/sha256"
	"fmt"
	"os"

	"github.com/shibukawa/tinygodriver/internal/rsasign"
)

func main() {
	pemBytes, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Println("read:", err)
		return
	}
	key, err := rsasign.ParsePKCS8(pemBytes)
	if err != nil {
		fmt.Println("parse:", err)
		return
	}
	defer key.Close()
	sum := sha256.Sum256([]byte("tinygodriver rsa-signer known-answer vector"))
	sig, err := key.SignPKCS1v15SHA256(sum[:])
	if err != nil {
		fmt.Println("sign:", err)
		return
	}
	fmt.Printf("backend=%s bits=%d siglen=%d head=%x\n", rsasign.Backend, key.Bits(), len(sig), sig[:8])
}
