// Command httpsdemo exercises the https package and reports what the current
// platform does.
//
// The point of this program is that there is nothing platform-specific in it.
// The same source builds and runs under standard Go and TinyGo, on macOS and
// Linux, with no build tags and no conditional imports. Only the TLS backend
// underneath differs, and only the client-certificate check below can tell.
//
//	go run ./examples/httpsdemo
//	tinygo build -o httpsdemo ./examples/httpsdemo && ./httpsdemo
//
// Set URL to point it somewhere else. It needs outbound HTTPS.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"time"

	"github.com/shibukawa/tinygodriver/https"
)

func main() {
	url := os.Getenv("URL")
	if url == "" {
		url = "https://example.com/"
	}

	fmt.Printf("platform : %s/%s, compiler %s\n", runtime.GOOS, runtime.GOARCH, runtime.Compiler)
	fmt.Printf("target   : %s\n\n", url)

	results := []bool{
		checkSystemTrust(url),
		checkVerificationIsEnforced(url),
		checkSkipVerify(url),
		checkClientCertSupport(url),
		checkTimeout(url),
	}

	failed := 0
	for _, ok := range results {
		if !ok {
			failed++
		}
	}
	fmt.Println()
	if failed > 0 {
		fmt.Printf("%d of %d checks failed\n", failed, len(results))
		os.Exit(1)
	}
	fmt.Printf("all %d checks passed\n", len(results))
}

// checkSystemTrust is the ordinary case: verify against the OS trust store.
// On Linux there is no OS trust store, so the package reads the distribution
// CA bundle and passes it to mbedTLS; the caller cannot tell.
func checkSystemTrust(url string) bool {
	resp, err := https.Get(url)
	if err != nil {
		return report("system trust", false, "%v", err)
	}
	defer resp.Body.Close()

	n, err := io.Copy(io.Discard, resp.Body)
	if err != nil {
		return report("system trust", false, "reading body: %v", err)
	}
	return report("system trust", true, "%s, %d bytes", resp.Status, n)
}

// checkVerificationIsEnforced proves the connection is actually verified.
// RootCAsOnly with no anchors trusts nothing, so a success here would mean
// verification is being skipped somewhere.
func checkVerificationIsEnforced(url string) bool {
	client := https.NewClient(https.WithRootCAsOnly(true))
	resp, err := client.Get(url)
	if err == nil {
		resp.Body.Close()
		return report("verification enforced", false,
			"connected with an empty trust store, which must never happen")
	}
	switch {
	case errors.Is(err, https.ErrUntrustedRoot):
		return report("verification enforced", true, "rejected: untrusted root")
	case errors.Is(err, https.ErrCertificateInvalid):
		return report("verification enforced", true, "rejected: certificate invalid")
	default:
		return report("verification enforced", true, "rejected: %v", err)
	}
}

// checkSkipVerify shows the escape hatch works, and is why it is named
// Insecure.
func checkSkipVerify(url string) bool {
	client := https.NewClient(https.WithInsecureSkipVerify(true))
	resp, err := client.Get(url)
	if err != nil {
		return report("skip verify", false, "%v", err)
	}
	resp.Body.Close()
	return report("skip verify", true, "%s", resp.Status)
}

// checkClientCertSupport is the one place platforms differ. Network.framework
// needs a SecIdentityRef, which only a keychain can produce, so the darwin
// backend refuses a client certificate rather than ignoring it. The deliberately
// invalid PEM below never reaches the network on any platform.
func checkClientCertSupport(url string) bool {
	client := https.NewClient(
		https.WithClientCertificate([]byte("not a certificate"), []byte("not a key")),
	)
	resp, err := client.Get(url)
	if err == nil {
		resp.Body.Close()
		return report("client certificates", false,
			"invalid PEM was accepted, which must never happen")
	}
	if errors.Is(err, https.ErrClientCertificateUnsupported) {
		return report("client certificates", true,
			"not supported on this backend, and refused rather than ignored")
	}
	// Any other error means the backend tried to use the certificate, which is
	// what a supporting backend should do with invalid PEM.
	return report("client certificates", true, "supported (invalid PEM rejected: %v)", err)
}

// checkTimeout shows a deadline reaching the TLS layer. It uses a request
// context rather than http.Client.Timeout, because TinyGo's net/http drops the
// setRequestCancel machinery that carries Client.Timeout to a custom
// RoundTripper; a context deadline works on every compiler.
func checkTimeout(url string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return report("deadline", false, "%v", err)
	}

	start := time.Now()
	resp, err := https.DefaultClient.Do(req)
	elapsed := time.Since(start)
	if err == nil {
		resp.Body.Close()
		return report("deadline", false, "a 1ms deadline somehow completed")
	}
	if elapsed > 5*time.Second {
		return report("deadline", false, "took %v, so the deadline was ignored", elapsed)
	}
	return report("deadline", true, "gave up after %v", elapsed.Round(time.Millisecond))
}

func report(name string, ok bool, format string, args ...any) bool {
	mark := "FAIL"
	if ok {
		mark = "ok  "
	}
	fmt.Printf("%s %-22s %s\n", mark, name, fmt.Sprintf(format, args...))
	return ok
}

// Compile-time reminder that the package hands back stock net/http types.
var _ *http.Client = https.DefaultClient
