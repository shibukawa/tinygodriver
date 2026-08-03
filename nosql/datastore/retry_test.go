package datastore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shibukawa/tinygodriver/cloud/google"
)

func TestRetryableStatusesAreRetried(t *testing.T) {
	for _, status := range []string{"UNAVAILABLE", "DEADLINE_EXCEEDED", "RESOURCE_EXHAUSTED"} {
		t.Run(status, func(t *testing.T) {
			s := newStub(
				stubReply{503, errorBody(status, "try again")},
				stubReply{503, errorBody(status, "try again")},
				stubReply{200, `{"found":[{"entity":{"key":{"path":[{"kind":"K","name":"a"}]},"properties":{}}}]}`},
			)
			client, _ := newTestClient(t, s)
			if _, err := client.Get(context.Background(), NameKey("K", "a")); err != nil {
				t.Fatalf("Get: %v", err)
			}
			if got := len(s.calls()); got != 3 {
				t.Errorf("%d attempts, want 3", got)
			}
		})
	}
}

func TestTerminalStatusesAreNotRetried(t *testing.T) {
	for _, c := range []struct {
		status string
		code   int
	}{
		{"INVALID_ARGUMENT", 400},
		{"FAILED_PRECONDITION", 400},
		{"NOT_FOUND", 404},
		{"ALREADY_EXISTS", 409},
		{"PERMISSION_DENIED", 403},
	} {
		t.Run(c.status, func(t *testing.T) {
			s := newStub(stubReply{c.code, errorBody(c.status, "no")})
			client, _ := newTestClient(t, s)
			_, err := client.Get(context.Background(), NameKey("K", "a"))
			if err == nil {
				t.Fatal("expected an error")
			}
			if !errors.Is(err, sentinelFor(c.status)) {
				t.Errorf("err = %v, want the %s sentinel", err, c.status)
			}
			if got := len(s.calls()); got != 1 {
				t.Errorf("%d attempts; %s is terminal", got, c.status)
			}
		})
	}
}

// TestAbortedIsTerminalOutsideATransaction pins the half of the ABORTED rule
// that is easy to get wrong. Inside a transaction the closure re-runs; outside
// one there is nothing to re-run, so resending would be pointless.
func TestAbortedIsTerminalOutsideATransaction(t *testing.T) {
	s := newStub(stubReply{409, errorBody("ABORTED", "contention")})
	client, _ := newTestClient(t, s)

	_, err := client.Put(context.Background(), NewEntity(NameKey("K", "n")))
	if !errors.Is(err, ErrAborted) {
		t.Fatalf("err = %v, want ErrAborted", err)
	}
	if got := len(s.calls()); got != 1 {
		t.Errorf("%d attempts; ABORTED outside a transaction is terminal", got)
	}
}

// TestInternalIsRetriedExactlyOnce follows Google's documented "do not retry
// more than once", which is neither the retryable nor the terminal rule.
func TestInternalIsRetriedExactlyOnce(t *testing.T) {
	s := newStub(stubReply{500, errorBody("INTERNAL", "oops")})
	client, _ := newTestClient(t, s)

	_, err := client.Get(context.Background(), NameKey("K", "a"))
	if !errors.Is(err, ErrInternal) {
		t.Fatalf("err = %v", err)
	}
	if got := len(s.calls()); got != 2 {
		t.Errorf("%d attempts, want exactly 2", got)
	}
}

// TestStatusBeatsHTTPCode is the reason classification keys on the status
// string: these two share a code and mean opposite things.
func TestStatusBeatsHTTPCode(t *testing.T) {
	aborted := &Error{StatusCode: 409, Status: "ABORTED"}
	exists := &Error{StatusCode: 409, Status: "ALREADY_EXISTS"}
	if !errors.Is(aborted, ErrAborted) || !errors.Is(exists, ErrAlreadyExists) {
		t.Fatal("409 was not discriminated by status")
	}
	if aborted.Retryable() {
		t.Error("ABORTED reported retryable at the request level")
	}
	if exists.Retryable() {
		t.Error("ALREADY_EXISTS reported retryable")
	}
}

func TestBodylessErrorFallsBackToTheCode(t *testing.T) {
	s := newStub(stubReply{503, ``})
	client, _ := newTestClient(t, s)

	_, err := client.Get(context.Background(), NameKey("K", "a"))
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable from the status code alone", err)
	}
}

// TestRetryStopsOnContextDeadline checks that backoff does not outlive the
// caller's deadline.
func TestRetryStopsOnContextDeadline(t *testing.T) {
	s := newStub(stubReply{503, errorBody("UNAVAILABLE", "later")})
	client, _ := newTestClient(t, s, WithRetry(10, 50*time.Millisecond))
	client.randFloat = func() float64 { return 1 } // full backoff, no jitter shortcut

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := client.Get(ctx, NameKey("K", "a"))
	if err == nil {
		t.Fatal("expected an error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want the context error", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("kept retrying for %v past the deadline", elapsed)
	}
}

// expiringSource hands out a token once, then a different one, so a refresh is
// observable.
type expiringSource struct{ issued int }

func (e *expiringSource) Token(context.Context) (google.Token, error) {
	e.issued++
	return google.Token{
		Value:  string(rune('a'+e.issued-1)) + "-token",
		Expiry: time.Now().Add(time.Hour),
	}, nil
}

// TestUnauthenticatedRefreshesOnce covers the case a wrong device clock
// produces: the server rejects a token this client believed was current.
func TestUnauthenticatedRefreshesOnce(t *testing.T) {
	s := newStub(
		stubReply{401, errorBody("UNAUTHENTICATED", "token expired")},
		stubReply{200, `{"found":[{"entity":{"key":{"path":[{"kind":"K","name":"a"}]},"properties":{}}}]}`},
	)
	inner := &expiringSource{}
	cache := google.Cached(inner)
	client, _ := newTestClient(t, s, WithTokenSource(cache))
	// newTestClient's default source is replaced above, but the refresh path
	// keys on the client's own cache, so wire it up the way New would.
	client.cache = cache

	if _, err := client.Get(context.Background(), NameKey("K", "a")); err != nil {
		t.Fatalf("Get: %v", err)
	}
	calls := s.calls()
	if len(calls) != 2 {
		t.Fatalf("%d attempts, want 2", len(calls))
	}
	if calls[0].Auth == calls[1].Auth {
		t.Errorf("the token was not refreshed: %q twice", calls[0].Auth)
	}
	if inner.issued != 2 {
		t.Errorf("inner source issued %d tokens, want 2", inner.issued)
	}
}

func TestUnauthenticatedGivesUpAfterOneRefresh(t *testing.T) {
	s := newStub(stubReply{401, errorBody("UNAUTHENTICATED", "still no")})
	cache := google.Cached(&expiringSource{})
	client, _ := newTestClient(t, s, WithTokenSource(cache))
	client.cache = cache

	_, err := client.Get(context.Background(), NameKey("K", "a"))
	if !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("err = %v", err)
	}
	if got := len(s.calls()); got != 2 {
		t.Errorf("%d attempts, want 2: one refresh and no more", got)
	}
}

func TestRetryDisabled(t *testing.T) {
	s := newStub(stubReply{503, errorBody("UNAVAILABLE", "later")})
	client, _ := newTestClient(t, s, WithRetry(0, 0))

	if _, err := client.Get(context.Background(), NameKey("K", "a")); err == nil {
		t.Fatal("expected an error")
	}
	if got := len(s.calls()); got != 1 {
		t.Errorf("%d attempts with retries disabled", got)
	}
}
