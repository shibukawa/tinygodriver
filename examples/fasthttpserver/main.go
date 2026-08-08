// Command fasthttpserver is a fasthttp server that works with both TinyGo and
// standard Go. Under TinyGo, the blank import registers the host Netdever so the
// net package can use the OS TCP stack, and -tags noasm is required because
// TinyGo cannot link klauspost/compress's zstd assembly.
//
//	tinygo build -tags noasm -o server ./examples/fasthttpserver && ./server
//	go run ./examples/fasthttpserver
//
// Optional environment variables:
//
//	ADDR    listen address (default 127.0.0.1:8080; port 0 does not work under
//	        TinyGo, whose Listener reports the address it was asked for)
//	STATIC  directory to serve under /static/ (default: route disabled)
package main

import (
	"bufio"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/shibukawa/tinygodriver/fasthttp"

	// Registers the host Netdever for TinyGo's net package.
	_ "github.com/shibukawa/tinygodriver/netdev"
)

func main() {
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = "127.0.0.1:8080"
	}

	var static fasthttp.RequestHandler
	if dir := os.Getenv("STATIC"); dir != "" {
		// FSHandler copies through a pooled buffer on TinyGo: the sendfile fast
		// paths need ReadFrom on os.File, which TinyGo does not implement.
		static = fasthttp.FSHandler(dir, 1)
	}

	srv := &fasthttp.Server{
		// CompressHandlerBrotliLevel negotiates br, gzip, deflate and zstd. All
		// four work under TinyGo; all four are also why the binary is 4 MB
		// larger than the net/http equivalent.
		Handler:      fasthttp.CompressHandlerBrotliLevel(router(static), 4, 6),
		Name:         "tinygodriver",
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	fmt.Printf("tinygodriver fasthttpserver listening on %s (runtime=%s compiler=%s backend=%s)\n",
		addr, runtime.GOOS+"/"+runtime.GOARCH, runtime.Compiler, fasthttp.Backend)
	fmt.Println("  GET  /          hello + request info")
	fmt.Println("  GET  /healthz   liveness probe")
	fmt.Println("  POST /echo      body echo (text/plain)")
	fmt.Println("  GET  /stream    chunked response, one line at a time")
	if static != nil {
		fmt.Printf("  GET  /static/*  files from %s\n", os.Getenv("STATIC"))
	}

	// Shutdown drains in-flight requests and makes ListenAndServe return nil.
	// That last part needs netdev to report a closed listener as such; a raw
	// errno would look like a crash instead.
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		<-sig
		fmt.Println("\nshutting down")
		if err := srv.Shutdown(); err != nil {
			fmt.Fprintln(os.Stderr, "shutdown error:", err)
		}
	}()

	if err := srv.ListenAndServe(addr); err != nil {
		fmt.Fprintln(os.Stderr, "server error:", err)
		os.Exit(1)
	}
}

func router(static fasthttp.RequestHandler) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		path := string(ctx.Path())
		switch {
		case path == "/":
			home(ctx)
		case path == "/healthz":
			ctx.SetContentType("text/plain; charset=utf-8")
			ctx.SetBodyString("ok\n")
		case path == "/echo":
			echo(ctx)
		case path == "/stream":
			stream(ctx)
		case static != nil && len(path) >= 8 && path[:8] == "/static/":
			static(ctx)
		default:
			ctx.Error("not found\n", fasthttp.StatusNotFound)
		}
	}
}

func home(ctx *fasthttp.RequestCtx) {
	ctx.SetContentType("text/plain; charset=utf-8")
	fmt.Fprintf(ctx, "hello from %s/%s via %s\n",
		runtime.GOOS, runtime.GOARCH, runtime.Compiler)
	fmt.Fprintf(ctx, "method:     %s\n", ctx.Method())
	fmt.Fprintf(ctx, "uri:        %s\n", ctx.RequestURI())
	fmt.Fprintf(ctx, "remote:     %s\n", ctx.RemoteIP())
	fmt.Fprintf(ctx, "user agent: %s\n", ctx.UserAgent())
	// Always false here: TinyGo cannot terminate TLS, so a server that needs it
	// belongs behind a terminator.
	fmt.Fprintf(ctx, "tls:        %v\n", ctx.IsTLS())
}

func echo(ctx *fasthttp.RequestCtx) {
	if !ctx.IsPost() {
		ctx.Error("POST only\n", fasthttp.StatusMethodNotAllowed)
		return
	}
	ctx.SetContentType("text/plain; charset=utf-8")
	ctx.SetBody(ctx.PostBody())
}

func stream(ctx *fasthttp.RequestCtx) {
	ctx.SetContentType("text/plain; charset=utf-8")
	ctx.SetBodyStreamWriter(func(w *bufio.Writer) {
		for i := 1; i <= 5; i++ {
			fmt.Fprintf(w, "line %d\n", i)
			if err := w.Flush(); err != nil {
				// The client hung up. Without netdev's SO_NOSIGPIPE this would
				// have been a signal rather than an error, and fatal.
				return
			}
			time.Sleep(200 * time.Millisecond)
		}
	})
}
