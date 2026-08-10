//go:build !tinygo

// These tests run under host Go. Without tags they exercise the standard-Go
// path; with -tags force_tinygo_logic they exercise the TinyGo path, so it is
// testable without a TinyGo toolchain. Nothing here is specific to either, on
// purpose: the two must not diverge.
package dynamodb_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shibukawa/tinygodriver/cloud/aws"
	"github.com/shibukawa/tinygodriver/nosql/dynamodb"
)

// capture records what the fake endpoint received.
type capture struct {
	Method     string
	Path       string
	Target     string
	Type       string
	Encoding   string
	Authz      string
	ContentSHA string
	Body       map[string]any
	Raw        []byte
}

type server struct {
	*httptest.Server
	mu    sync.Mutex
	seen  []capture
	conns int
}

// requests returns what the endpoint received so far.
func (s *server) requests() []capture {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]capture(nil), s.seen...)
}

func (s *server) connections() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conns
}

// newServer starts a fake DynamoDB endpoint. handler writes the reply; the
// request is recorded first so tests can assert on how it was built and signed.
func newServer(t *testing.T, handler func(w http.ResponseWriter, r *http.Request, n int)) *server {
	t.Helper()
	s := &server{}
	s.Server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		json.Unmarshal(raw, &body)

		s.mu.Lock()
		s.seen = append(s.seen, capture{
			Method:     r.Method,
			Path:       r.URL.Path,
			Target:     r.Header.Get("X-Amz-Target"),
			Type:       r.Header.Get("Content-Type"),
			Encoding:   r.Header.Get("Accept-Encoding"),
			Authz:      r.Header.Get("Authorization"),
			ContentSHA: r.Header.Get("X-Amz-Content-Sha256"),
			Body:       body,
			Raw:        raw,
		})
		n := len(s.seen)
		s.mu.Unlock()

		handler(w, r, n)
	}))
	s.Server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			s.mu.Lock()
			s.conns++
			s.mu.Unlock()
		}
	}
	s.Start()
	t.Cleanup(s.Close)
	return s
}

// writeJSON replies with body and the checksum header DynamoDB sends.
func writeJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/x-amz-json-1.0")
	w.Header().Set("X-Amz-Crc32", strconv.FormatUint(uint64(crc32.ChecksumIEEE([]byte(body))), 10))
	w.Header().Set("X-Amzn-Requestid", "REQ123")
	w.WriteHeader(status)
	io.WriteString(w, body)
}

// exception replies with the error document shape DynamoDB uses.
func exception(w http.ResponseWriter, status int, typ, message string) {
	writeJSON(w, status, fmt.Sprintf(`{"__type":%q,"message":%q}`, typ, message))
}

func newClient(t *testing.T, endpoint string, opts ...dynamodb.Option) *dynamodb.Client {
	t.Helper()
	options := append([]dynamodb.Option{
		dynamodb.WithEndpoint(endpoint),
		dynamodb.WithRegion("ap-northeast-1"),
		dynamodb.WithCredentials(aws.Credentials{AccessKeyID: "id", SecretAccessKey: "secret"}),
	}, opts...)
	client, err := dynamodb.New(options...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	return client
}

func TestGetItemRequestShape(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request, n int) {
		writeJSON(w, 200, `{"Item":{"pk":{"S":"u#1"},"age":{"N":"42"}}}`)
	})
	client := newClient(t, srv.URL)

	item, err := client.GetItem(context.Background(), "users",
		dynamodb.Key{"pk": dynamodb.S("u#1")}, dynamodb.WithConsistentRead(true))
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if v, ok := item["age"].AsInt(); !ok || v != 42 {
		t.Errorf("age = %d, %v", v, ok)
	}

	got := srv.requests()[0]
	if got.Method != http.MethodPost || got.Path != "/" {
		t.Errorf("request line = %s %s, want POST /", got.Method, got.Path)
	}
	if got.Target != "DynamoDB_20120810.GetItem" {
		t.Errorf("X-Amz-Target = %q", got.Target)
	}
	if got.Type != "application/x-amz-json-1.0" {
		t.Errorf("Content-Type = %q", got.Type)
	}
	if got.Encoding != "identity" {
		t.Errorf("Accept-Encoding = %q, want identity so the checksum covers what arrived", got.Encoding)
	}
	if !strings.Contains(got.Authz, "/ap-northeast-1/dynamodb/aws4_request") {
		t.Errorf("Authorization does not carry the dynamodb scope: %s", got.Authz)
	}
	if got.ContentSHA == "" {
		t.Error("X-Amz-Content-Sha256 is absent, so the body is not covered by the signature")
	}
	if got.Body["TableName"] != "users" {
		t.Errorf("TableName = %v", got.Body["TableName"])
	}
	if got.Body["ConsistentRead"] != true {
		t.Errorf("ConsistentRead = %v", got.Body["ConsistentRead"])
	}
}

func TestOperationTimeoutIncludesRetryBackoffWithCustomHTTPClient(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request, n int) {
		exception(w, http.StatusBadRequest, "ThrottlingException", "slow down")
	})
	client := newClient(t, srv.URL,
		dynamodb.WithHTTPClient(srv.Client()),
		dynamodb.WithTimeout(20*time.Millisecond),
		dynamodb.WithRetry(3, 100*time.Millisecond))

	started := time.Now()
	_, err := client.GetItem(context.Background(), "users", dynamodb.Key{"pk": dynamodb.S("1")})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("GetItem error = %v, want context deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("operation took %v, timeout did not bound retry backoff", elapsed)
	}
}

func TestCustomHTTPClientRejectsPoolOptions(t *testing.T) {
	_, err := dynamodb.New(
		dynamodb.WithEndpoint("http://127.0.0.1:1"),
		dynamodb.WithRegion("us-east-1"),
		dynamodb.WithCredentials(aws.Credentials{AccessKeyID: "id", SecretAccessKey: "secret"}),
		dynamodb.WithHTTPClient(&http.Client{}),
		dynamodb.WithMaxIdleConns(8),
	)
	if !errors.Is(err, dynamodb.ErrHTTPClientOwnership) {
		t.Fatalf("New error = %v, want ErrHTTPClientOwnership", err)
	}
}

// TestGetItemMissing covers the 200-with-no-Item reply, which is how DynamoDB
// reports a miss.
func TestGetItemMissing(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request, n int) {
		writeJSON(w, 200, `{}`)
	})
	client := newClient(t, srv.URL)

	item, err := client.GetItem(context.Background(), "users", dynamodb.Key{"pk": dynamodb.S("nope")})
	if !errors.Is(err, dynamodb.ErrItemNotFound) {
		t.Errorf("err = %v, want ErrItemNotFound", err)
	}
	if item != nil {
		t.Errorf("item = %v, want nil", item)
	}
}

func TestErrorMapping(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		typ    string
		want   error
	}{
		{"not found", 400, "com.amazonaws.dynamodb.v20120810#ResourceNotFoundException", dynamodb.ErrResourceNotFound},
		{"condition", 400, "com.amazonaws.dynamodb.v20120810#ConditionalCheckFailedException", dynamodb.ErrConditionalCheck},
		{"validation", 400, "com.amazonaws.dynamodb.v20120810#ValidationException", dynamodb.ErrValidation},
		{"throughput", 400, "com.amazonaws.dynamodb.v20120810#ProvisionedThroughputExceededException", dynamodb.ErrThroughputExceeded},
		// The auth failures arrive under the coral namespace, not the dynamodb
		// one, which is why the mapping keys on the name after the "#".
		{"missing auth", 400, "com.amazon.coral.service#MissingAuthenticationTokenException", dynamodb.ErrBadCredentials},
		{"bad signature", 403, "com.amazon.coral.service#InvalidSignatureException", dynamodb.ErrBadCredentials},
		{"server", 500, "com.amazonaws.dynamodb.v20120810#InternalServerError", dynamodb.ErrServerFailure},
	} {
		t.Run(test.name, func(t *testing.T) {
			srv := newServer(t, func(w http.ResponseWriter, r *http.Request, n int) {
				exception(w, test.status, test.typ, "boom")
			})
			client := newClient(t, srv.URL, dynamodb.WithRetry(1, 0))

			_, err := client.GetItem(context.Background(), "users", dynamodb.Key{"pk": dynamodb.S("k")})
			if !errors.Is(err, test.want) {
				t.Fatalf("err = %v, want %v", err, test.want)
			}

			var e *dynamodb.Error
			if !errors.As(err, &e) {
				t.Fatalf("err is %T, want *dynamodb.Error", err)
			}
			if e.Op != "GetItem" || e.Table != "users" || e.StatusCode != test.status {
				t.Errorf("error fields = %+v", e)
			}
			if strings.Contains(e.Type, "#") {
				t.Errorf("Type = %q, want the name without its namespace", e.Type)
			}
			if e.RequestID != "REQ123" {
				t.Errorf("RequestID = %q", e.RequestID)
			}
			if e.Message != "boom" {
				t.Errorf("Message = %q", e.Message)
			}
		})
	}
}

// TestErrorWithoutDocument covers a reply from something in the middle, a proxy
// or a load balancer, that is not DynamoDB.
func TestErrorWithoutDocument(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request, n int) {
		w.WriteHeader(503)
		io.WriteString(w, "<html>service unavailable</html>")
	})
	client := newClient(t, srv.URL, dynamodb.WithRetry(1, 0))

	_, err := client.GetItem(context.Background(), "users", dynamodb.Key{"pk": dynamodb.S("k")})
	if !errors.Is(err, dynamodb.ErrServerFailure) {
		t.Errorf("err = %v, want ErrServerFailure", err)
	}
}

func TestThrottlingIsRetried(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request, n int) {
		if n < 3 {
			exception(w, 400, "com.amazonaws.dynamodb.v20120810#ThrottlingException", "slow down")
			return
		}
		writeJSON(w, 200, `{"Item":{"pk":{"S":"u#1"}}}`)
	})
	client := newClient(t, srv.URL, dynamodb.WithRetry(3, time.Millisecond))

	if _, err := client.GetItem(context.Background(), "users", dynamodb.Key{"pk": dynamodb.S("u#1")}); err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if got := len(srv.requests()); got != 3 {
		t.Errorf("sent %d requests, want 3", got)
	}
}

// TestValidationIsNotRetried is the other half: a request the server has
// already judged wrong will be just as wrong the second time.
func TestValidationIsNotRetried(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request, n int) {
		exception(w, 400, "com.amazonaws.dynamodb.v20120810#ValidationException", "bad expression")
	})
	client := newClient(t, srv.URL, dynamodb.WithRetry(3, time.Millisecond))

	_, err := client.GetItem(context.Background(), "users", dynamodb.Key{"pk": dynamodb.S("k")})
	if !errors.Is(err, dynamodb.ErrValidation) {
		t.Fatalf("err = %v", err)
	}
	if got := len(srv.requests()); got != 1 {
		t.Errorf("sent %d requests, want 1", got)
	}
}

func TestRetryExhausts(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request, n int) {
		exception(w, 400, "com.amazonaws.dynamodb.v20120810#ThrottlingException", "slow down")
	})
	client := newClient(t, srv.URL, dynamodb.WithRetry(3, time.Millisecond))

	_, err := client.GetItem(context.Background(), "users", dynamodb.Key{"pk": dynamodb.S("k")})
	if !errors.Is(err, dynamodb.ErrThrottled) {
		t.Fatalf("err = %v, want ErrThrottled", err)
	}
	if got := len(srv.requests()); got != 3 {
		t.Errorf("sent %d requests, want the configured 3", got)
	}
}

// TestRetryStopsOnContext asserts the caller's deadline outranks the retry
// budget: backoff waits on the context, it does not sleep through it.
func TestRetryStopsOnContext(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request, n int) {
		exception(w, 400, "com.amazonaws.dynamodb.v20120810#ThrottlingException", "slow down")
	})
	client := newClient(t, srv.URL, dynamodb.WithRetry(10, 200*time.Millisecond))

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := client.GetItem(ctx, "users", dynamodb.Key{"pk": dynamodb.S("k")})
	if err == nil {
		t.Fatal("GetItem succeeded")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("returned after %v, so it slept through the deadline", elapsed)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want the context error", err)
	}
}

func TestChecksumMismatchIsRetriedThenReported(t *testing.T) {
	const body = `{"Item":{"pk":{"S":"u#1"}}}`
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request, n int) {
		w.Header().Set("X-Amz-Crc32", "1") // never the real checksum
		w.WriteHeader(200)
		io.WriteString(w, body)
	})
	client := newClient(t, srv.URL, dynamodb.WithRetry(2, time.Millisecond))

	_, err := client.GetItem(context.Background(), "users", dynamodb.Key{"pk": dynamodb.S("u#1")})
	if !errors.Is(err, dynamodb.ErrChecksumMismatch) {
		t.Fatalf("err = %v, want ErrChecksumMismatch", err)
	}
	if got := len(srv.requests()); got != 2 {
		t.Errorf("sent %d requests, want 2: a corrupted reply is worth retrying", got)
	}
}

// TestChecksumAbsentIsAccepted covers a reply with no checksum header, which is
// what a proxy that strips it produces. Both DynamoDB and DynamoDB Local send
// one, so this is tolerance for the middle of the network, not for the server.
func TestChecksumAbsentIsAccepted(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request, n int) {
		w.WriteHeader(200)
		io.WriteString(w, `{"Item":{"pk":{"S":"u#1"}}}`)
	})
	client := newClient(t, srv.URL)

	if _, err := client.GetItem(context.Background(), "users", dynamodb.Key{"pk": dynamodb.S("u#1")}); err != nil {
		t.Fatalf("GetItem: %v", err)
	}
}

// TestConnectionReuse is why the client sets a pool size at all: without reuse
// every call pays a TLS handshake, which is an order of magnitude above the
// request itself.
func TestConnectionReuse(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request, n int) {
		writeJSON(w, 200, `{"Item":{"pk":{"S":"u#1"}}}`)
	})
	client := newClient(t, srv.URL)

	for i := 0; i < 3; i++ {
		if _, err := client.GetItem(context.Background(), "users", dynamodb.Key{"pk": dynamodb.S("u#1")}); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if got := srv.connections(); got != 1 {
		t.Errorf("server accepted %d connections for 3 requests, want 1", got)
	}
}

// TestCloseReleasesConnections asserts Close does what its doc says: the next
// request has to dial again.
func TestCloseReleasesConnections(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request, n int) {
		writeJSON(w, 200, `{"Item":{"pk":{"S":"u#1"}}}`)
	})
	client := newClient(t, srv.URL)
	key := dynamodb.Key{"pk": dynamodb.S("u#1")}

	if _, err := client.GetItem(context.Background(), "users", key); err != nil {
		t.Fatal(err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetItem(context.Background(), "users", key); err != nil {
		t.Fatal(err)
	}
	if got := srv.connections(); got != 2 {
		t.Errorf("server accepted %d connections, want 2: Close did not drop the pooled one", got)
	}
}

// TestPooledConnectionReplayDeliversTwice documents the hazard the retry
// documentation warns about, rather than leaving it as prose.
//
// A pooled connection can be closed by the peer while it sits idle, and nothing
// short of using it reveals that. The recovery is to send the request again,
// which cannot distinguish "the server never saw it" from "the server acted and
// the reply was lost", so a non-idempotent write can be applied twice.
//
// Where the second send comes from differs by build path, which is why this
// test carries no build tag: the native transport replays the request itself,
// while net/http declines to replay a POST whose bytes were written and leaves
// it to the retry in this client. The observable result is the same, and that
// is the point being pinned.
func TestPooledConnectionReplayDeliversTwice(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request, n int) {
		if n == 2 {
			// The request arrived and was "processed"; the connection then dies
			// before any reply byte, which is exactly the ambiguous case.
			hijacker, ok := w.(http.Hijacker)
			if !ok {
				t.Skip("server does not support hijacking")
			}
			conn, _, err := hijacker.Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			conn.Close()
			return
		}
		writeJSON(w, 200, `{}`)
	})
	client := newClient(t, srv.URL, dynamodb.WithRetry(dynamodb.DefaultAttempts, time.Millisecond))
	key := dynamodb.Key{"pk": dynamodb.S("u#1")}

	if _, err := client.PutItem(context.Background(), "counters", dynamodb.Item{"pk": dynamodb.S("u#1")}); err != nil {
		t.Fatalf("first call: %v", err)
	}
	// The second call leases the pooled connection, finds it dead, and replays.
	if _, err := client.UpdateItem(context.Background(), "counters", key, "ADD score :one",
		dynamodb.WithExpressionValues(map[string]dynamodb.AttributeValue{":one": dynamodb.N(1)})); err != nil {
		t.Fatalf("second call: %v", err)
	}

	updates := 0
	for _, req := range srv.requests() {
		if strings.HasSuffix(req.Target, ".UpdateItem") {
			updates++
		}
	}
	if updates != 2 {
		t.Errorf("the server saw the update %d times, want 2: this test exists to pin that it is 2, not 1", updates)
	}
}
