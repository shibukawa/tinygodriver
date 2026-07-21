package httprevproxy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type trackingBody struct {
	io.Reader
	closed bool
}

func (b *trackingBody) Close() error {
	b.closed = true
	return nil
}

type trackingPool struct {
	buffer []byte
	gets   int
	puts   int
}

type flushRecorder struct {
	*httptest.ResponseRecorder
	flushes int
}

func (r *flushRecorder) Flush() {
	r.flushes++
	r.ResponseRecorder.Flush()
}

func (p *trackingPool) Get() []byte {
	p.gets++
	return p.buffer
}

func (p *trackingPool) Put(buffer []byte) {
	p.puts++
	p.buffer = buffer
}

// This declaration intentionally exercises every public ReverseProxy field so
// accidental API drift is a compile failure.
var _ = ReverseProxy{
	Rewrite:        func(*ProxyRequest) {},
	Transport:      roundTripFunc(nil),
	FlushInterval:  time.Millisecond,
	ErrorLog:       log.New(io.Discard, "", 0),
	BufferPool:     &trackingPool{},
	ModifyResponse: func(*http.Response) error { return nil },
	ErrorHandler:   func(http.ResponseWriter, *http.Request, error) {},
	Director:       func(*http.Request) {},
}

func TestRewriteProxy(t *testing.T) {
	target, err := url.Parse("https://backend.example/base?from=target")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "http://frontend.example/a%2Fb?from=request", nil)
	request.RemoteAddr = "192.0.2.10:4321"
	request.Header.Set("Forwarded", "for=attacker")
	request.Header.Set("X-Forwarded-For", "attacker")
	request.Header.Set("Connection", "X-Remove")
	request.Header.Set("X-Remove", "secret")
	ctx := context.WithValue(request.Context(), contextKey{}, "present")
	request = request.WithContext(ctx)

	pool := &trackingPool{buffer: make([]byte, 8)}
	responseBody := &trackingBody{Reader: strings.NewReader("proxied")}
	proxy := &ReverseProxy{
		BufferPool: pool,
		Rewrite: func(proxyRequest *ProxyRequest) {
			if proxyRequest.In != request {
				t.Error("Rewrite In is not the inbound request")
			}
			if proxyRequest.Out.Header.Get("Forwarded") != "" || proxyRequest.Out.Header.Get("X-Forwarded-For") != "" {
				t.Error("untrusted forwarding headers were not removed before Rewrite")
			}
			proxyRequest.SetURL(target)
			proxyRequest.SetXForwarded()
		},
		Transport: roundTripFunc(func(out *http.Request) (*http.Response, error) {
			if out.Context().Value(contextKey{}) != "present" {
				t.Error("outbound request did not preserve context")
			}
			if got, want := out.URL.String(), "https://backend.example/base/a%2Fb?from=target&from=request"; got != want {
				t.Errorf("outbound URL = %q, want %q", got, want)
			}
			if out.Host != "" {
				t.Errorf("outbound Host = %q, want target host selection", out.Host)
			}
			if got := out.Header.Get("X-Forwarded-For"); got != "192.0.2.10" {
				t.Errorf("X-Forwarded-For = %q", got)
			}
			if got := out.Header.Get("X-Forwarded-Host"); got != "frontend.example" {
				t.Errorf("X-Forwarded-Host = %q", got)
			}
			if got := out.Header.Get("X-Forwarded-Proto"); got != "http" {
				t.Errorf("X-Forwarded-Proto = %q", got)
			}
			if out.Header.Get("X-Remove") != "" || out.Header.Get("Connection") != "" {
				t.Error("request hop-by-hop headers were forwarded")
			}
			return &http.Response{
				StatusCode:    http.StatusCreated,
				Header:        http.Header{"Connection": {"X-Backend-Hop"}, "X-Backend-Hop": {"secret"}, "X-End-To-End": {"yes"}},
				Body:          responseBody,
				ContentLength: 7,
				Trailer:       http.Header{"X-Checksum": {"abc"}},
			}, nil
		}),
	}

	recorder := httptest.NewRecorder()
	proxy.ServeHTTP(recorder, request)
	result := recorder.Result()
	defer result.Body.Close()
	body, err := io.ReadAll(result.Body)
	if err != nil {
		t.Fatal(err)
	}
	if result.StatusCode != http.StatusCreated || string(body) != "proxied" {
		t.Fatalf("response = (%d, %q)", result.StatusCode, body)
	}
	if result.Header.Get("X-End-To-End") != "yes" {
		t.Error("end-to-end response header was not copied")
	}
	if result.Header.Get("X-Backend-Hop") != "" || result.Header.Get("Connection") != "" {
		t.Error("response hop-by-hop headers were forwarded")
	}
	if result.Trailer.Get("X-Checksum") != "abc" {
		t.Errorf("trailer = %q", result.Trailer.Get("X-Checksum"))
	}
	if !responseBody.closed {
		t.Error("backend response body was not closed")
	}
	if pool.gets != 1 || pool.puts != 1 {
		t.Errorf("BufferPool calls = Get %d, Put %d", pool.gets, pool.puts)
	}
}

func TestNewSingleHostReverseProxyUsesDirectorCompatibility(t *testing.T) {
	target, _ := url.Parse("http://backend.example/base")
	proxy := NewSingleHostReverseProxy(target)
	if proxy.Director == nil || proxy.Rewrite != nil {
		t.Fatal("NewSingleHostReverseProxy must use Director for standard-library compatibility")
	}
	proxy.Transport = roundTripFunc(func(out *http.Request) (*http.Response, error) {
		if got, want := out.URL.String(), "http://backend.example/base/path"; got != want {
			t.Errorf("outbound URL = %q, want %q", got, want)
		}
		if got := out.Host; got != "frontend.example" {
			t.Errorf("outbound Host = %q", got)
		}
		if got := out.Header.Get("X-Forwarded-For"); got != "198.51.100.1, 192.0.2.20" {
			t.Errorf("X-Forwarded-For = %q", got)
		}
		return &http.Response{StatusCode: http.StatusNoContent, Header: make(http.Header), Body: http.NoBody}, nil
	})
	request := httptest.NewRequest(http.MethodGet, "http://frontend.example/path", nil)
	request.RemoteAddr = "192.0.2.20:1000"
	request.Header.Set("X-Forwarded-For", "198.51.100.1")
	recorder := httptest.NewRecorder()
	proxy.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Errorf("status = %d", recorder.Code)
	}
}

func TestModifyResponseErrorUsesErrorHandler(t *testing.T) {
	wantErr := errors.New("rejected response")
	body := &trackingBody{Reader: bytes.NewReader(nil)}
	called := false
	proxy := &ReverseProxy{
		Rewrite: func(r *ProxyRequest) {},
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: body}, nil
		}),
		ModifyResponse: func(*http.Response) error { return wantErr },
		ErrorHandler: func(rw http.ResponseWriter, req *http.Request, err error) {
			called = true
			if err != wantErr {
				t.Errorf("error = %v", err)
			}
			rw.WriteHeader(http.StatusServiceUnavailable)
		},
	}
	recorder := httptest.NewRecorder()
	proxy.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "http://example.test/", nil))
	if !called || recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("error handler called = %v, status = %d", called, recorder.Code)
	}
	if !body.closed {
		t.Error("response body was not closed after ModifyResponse error")
	}
}

func TestCancellationReachesTransport(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodGet, "http://example.test/", nil).WithContext(ctx)
	canceled := make(chan struct{})
	proxy := &ReverseProxy{
		Rewrite: func(*ProxyRequest) {},
		Transport: roundTripFunc(func(out *http.Request) (*http.Response, error) {
			<-out.Context().Done()
			close(canceled)
			return nil, out.Context().Err()
		}),
		ErrorHandler: func(rw http.ResponseWriter, req *http.Request, err error) {
			if !errors.Is(err, context.Canceled) {
				t.Errorf("transport error = %v", err)
			}
			rw.WriteHeader(http.StatusBadGateway)
		},
	}
	done := make(chan struct{})
	go func() {
		proxy.ServeHTTP(httptest.NewRecorder(), request)
		close(done)
	}()
	cancel()
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("transport did not observe cancellation")
	}
	<-done
}

func TestStreamingResponseFlushesImmediately(t *testing.T) {
	proxy := &ReverseProxy{
		Rewrite: func(*ProxyRequest) {},
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode:    http.StatusOK,
				Header:        http.Header{"Content-Type": {"text/event-stream"}},
				Body:          io.NopCloser(strings.NewReader("data: ready\n\n")),
				ContentLength: -1,
			}, nil
		}),
	}
	recorder := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	proxy.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "http://example.test/", nil))
	if recorder.flushes == 0 {
		t.Error("streaming response was not flushed")
	}
}

func TestConfigurationAndUpgradeErrors(t *testing.T) {
	tests := []struct {
		name  string
		proxy *ReverseProxy
		req   *http.Request
	}{
		{name: "neither callback", proxy: &ReverseProxy{}, req: httptest.NewRequest(http.MethodGet, "http://example.test/", nil)},
		{name: "both callbacks", proxy: &ReverseProxy{Director: func(*http.Request) {}, Rewrite: func(*ProxyRequest) {}}, req: httptest.NewRequest(http.MethodGet, "http://example.test/", nil)},
		{name: "upgrade", proxy: &ReverseProxy{Rewrite: func(*ProxyRequest) {}}, req: upgradeRequest()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			test.proxy.ServeHTTP(recorder, test.req)
			if recorder.Code != http.StatusBadGateway {
				t.Errorf("status = %d", recorder.Code)
			}
		})
	}
}

func upgradeRequest() *http.Request {
	request := httptest.NewRequest(http.MethodGet, "http://example.test/", nil)
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Upgrade", "websocket")
	return request
}

type contextKey struct{}
