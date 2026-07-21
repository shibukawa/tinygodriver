// Command httpserver is a minimal HTTP server that works with both TinyGo
// and standard Go. Under TinyGo, the blank import registers the host Netdever
// so net/http can use the OS TCP stack.
//
//	tinygo build -o server ./examples/httpserver && ./server
//	go run ./examples/httpserver
//
// Optional environment variables:
//
//	ADDR          listen address (default :8080)
//	UPSTREAM_URL  reverse-proxy target (default http://127.0.0.1:8081)
package main

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"time"

	"github.com/shibukawa/tinygodriver/httpmux"
	"github.com/shibukawa/tinygodriver/httprevproxy"

	// Registers the host Netdever for TinyGo's net package.
	_ "github.com/shibukawa/tinygodriver/netdev"
)

func main() {
	target, err := upstreamURL()
	if err != nil {
		fmt.Fprintln(os.Stderr, "invalid UPSTREAM_URL:", err)
		os.Exit(2)
	}

	mux := httpmux.NewServeMux()
	mux.HandleFunc("GET /{$}", homeHandler)
	mux.HandleFunc("GET /healthz", healthHandler)
	mux.HandleFunc("POST /echo", echoHandler)
	mux.Handle("/proxy/{path...}", reverseProxy(target))

	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}

	fmt.Printf("tinygodriver httpserver listening on %s (runtime=%s compiler=%s)\n",
		addr, runtime.GOOS+"/"+runtime.GOARCH, runtime.Compiler)
	fmt.Println("  GET  /         hello + request info")
	fmt.Println("  GET  /healthz  liveness probe")
	fmt.Println("  POST /echo     body echo (text/plain)")
	fmt.Printf("  ANY  /proxy/*  reverse proxy to %s\n", target)
	if err = http.ListenAndServe(addr, mux); err != nil {
		fmt.Fprintln(os.Stderr, "server error:", err)
		os.Exit(1)
	}
}

func upstreamURL() (*url.URL, error) {
	rawURL := os.Getenv("UPSTREAM_URL")
	if rawURL == "" {
		rawURL = "http://127.0.0.1:8081"
	}
	target, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	if target.Scheme == "" || target.Host == "" {
		return nil, fmt.Errorf("must include a scheme and host: %q", rawURL)
	}
	return target, nil
}

func reverseProxy(target *url.URL) http.Handler {
	return &httprevproxy.ReverseProxy{
		Rewrite: func(request *httprevproxy.ProxyRequest) {
			// Strip the sample's /proxy/ prefix before joining with any base
			// path configured in UPSTREAM_URL.
			request.Out.URL.Path = "/" + request.In.PathValue("path")
			request.Out.URL.RawPath = ""
			request.SetURL(target)
			request.SetXForwarded()
		},
	}
}

func homeHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintf(w, "hello from tinygodriver\n")
	fmt.Fprintf(w, "method=%s path=%q remote=%s\n", r.Method, r.URL.Path, r.RemoteAddr)
	fmt.Fprintf(w, "goos=%s goarch=%s compiler=%s\n", runtime.GOOS, runtime.GOARCH, runtime.Compiler)
	fmt.Fprintf(w, "time=%s\n", time.Now().UTC().Format(time.RFC3339))
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write([]byte("ok\n"))
	}
}

func echoHandler(w http.ResponseWriter, r *http.Request) {
	const maxBody = 64 << 10 // 64 KiB
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	_ = r.Body.Close()
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if len(body) > maxBody {
		http.Error(w, "body too large", http.StatusRequestEntityTooLarge)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Echo-Bytes", fmt.Sprintf("%d", len(body)))
	_, _ = w.Write(body)
}
