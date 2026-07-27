// Command httpsclient fetches an HTTPS URL using the https package, which
// performs TLS through the host OS stack. It builds with both TinyGo and
// standard Go.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/shibukawa/tinygodriver/https"
)

func main() {
	url := os.Getenv("URL")
	if url == "" {
		url = "https://example.com/"
	}

	var opts []https.Option
	if ca := os.Getenv("CA_FILE"); ca != "" {
		opts = append(opts, https.WithRootCAFile(ca))
	}
	if os.Getenv("INSECURE") == "1" {
		opts = append(opts, https.WithInsecureSkipVerify(true))
	}

	client := https.NewClient(opts...)
	resp, err := client.Get(url)
	if err != nil {
		fmt.Println("error:", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Println("read error:", err)
		os.Exit(1)
	}

	fmt.Println("status:", resp.Status)
	fmt.Println("bytes:", len(body))
	if len(body) > 200 {
		body = body[:200]
	}
	fmt.Printf("body: %s\n", body)
}
