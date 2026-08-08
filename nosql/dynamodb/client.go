package dynamodb

import (
	"bytes"
	"context"
	"encoding/json"
	"hash/crc32"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/shibukawa/tinygodriver/cloud/aws"
)

// Defaults for a client built without the matching option.
const (
	// DefaultTimeout bounds one request, including reading the reply. It is
	// short because these are small round trips: a call that has not finished
	// in ten seconds is not going to.
	DefaultTimeout = 10 * time.Second

	// DefaultMaxIdleConns is how many connections stay pooled. Every request
	// goes to one host, so this is the whole pool; a caller running more
	// operations at once should raise it with WithMaxIdleConns.
	DefaultMaxIdleConns = 4

	// DefaultAttempts is how many times a retryable failure is sent, the first
	// try included.
	DefaultAttempts = 3

	// DefaultRetryBase is the first backoff delay, doubled per attempt and
	// capped at retryCap.
	DefaultRetryBase = 25 * time.Millisecond

	retryCap = time.Second

	// apiVersion is the target prefix of the JSON protocol.
	apiVersion = "DynamoDB_20120810."

	// contentType is the JSON 1.0 protocol DynamoDB speaks.
	contentType = "application/x-amz-json-1.0"

	// maxResponseBody bounds a reply. A BatchGetItem answer tops out at 16 MB,
	// so this is generous while still bounded.
	maxResponseBody = 32 << 20
)

// Client talks to one DynamoDB endpoint. It is safe for concurrent use.
type Client struct {
	endpoint *url.URL
	region   string
	creds    aws.Credentials
	http     *http.Client
	ownsHTTP bool
	attempts int
	base     time.Duration
	now      func() time.Time // overridden in tests
	sleep    func(context.Context, time.Duration) error
}

type config struct {
	endpoint     string
	region       string
	creds        aws.Credentials
	credsSet     bool
	timeout      time.Duration
	maxIdleConns int
	attempts     int
	retryBase    time.Duration
	httpClient   *http.Client
}

// Option configures a Client.
type Option func(*config)

// WithEndpoint overrides the endpoint URL, for DynamoDB Local or a compatible
// server. It defaults to AWS_ENDPOINT_URL_DYNAMODB, AWS_ENDPOINT_URL, or the
// regional AWS endpoint.
func WithEndpoint(endpoint string) Option {
	return func(c *config) { c.endpoint = endpoint }
}

// WithRegion sets the signing region. It defaults to AWS_REGION, then
// AWS_DEFAULT_REGION.
func WithRegion(region string) Option {
	return func(c *config) { c.region = region }
}

// WithCredentials sets static credentials.
func WithCredentials(creds aws.Credentials) Option {
	return func(c *config) {
		c.creds = creds
		c.credsSet = true
	}
}

// WithCredentialsFromEnv reads credentials from the environment. New does this
// already when no credentials option is given; the option states it explicitly.
func WithCredentialsFromEnv() Option {
	return func(c *config) {
		c.creds = aws.CredentialsFromEnv()
		c.credsSet = true
	}
}

// WithTimeout bounds each request, including reading the reply and any retry
// backoff. Zero means DefaultTimeout.
func WithTimeout(timeout time.Duration) Option {
	return func(c *config) { c.timeout = timeout }
}

// WithMaxIdleConns sets how many connections stay pooled for the endpoint.
//
// Set it to the number of operations the program runs at once: every request
// goes to the same host, so this cap is the whole pool. A call that finds no
// pooled connection pays a TLS handshake, which is roughly ten times the cost
// of the request itself.
func WithMaxIdleConns(n int) Option {
	return func(c *config) { c.maxIdleConns = n }
}

// WithRetry sets how many times a retryable failure is sent, the first try
// included, and the first backoff delay. Attempts of 0 or 1 disables retrying;
// a zero base means DefaultRetryBase.
//
// Retries are not free of consequence. A request can be delivered and its reply
// lost, and the transport underneath replays a request once when a pooled
// connection turns out to be dead, so a write can reach the table up to
// attempts x 2 times. PutItem and a plain UpdateItem SET are idempotent; an
// UpdateItem with ADD is not, and should carry a condition expression.
func WithRetry(attempts int, base time.Duration) Option {
	return func(c *config) {
		c.attempts = attempts
		c.retryBase = base
	}
}

// WithHTTPClient supplies the http.Client to use. Close does not shut down a
// client supplied this way.
func WithHTTPClient(client *http.Client) Option {
	return func(c *config) { c.httpClient = client }
}

// New builds a Client. Region, endpoint, and credentials fall back to the
// environment, so a configured shell needs no options at all.
func New(opts ...Option) (*Client, error) {
	cfg := &config{
		endpoint:     aws.EndpointFromEnv("dynamodb"),
		region:       aws.RegionFromEnv(),
		maxIdleConns: DefaultMaxIdleConns,
		attempts:     DefaultAttempts,
		retryBase:    DefaultRetryBase,
	}
	for _, opt := range opts {
		opt(cfg)
	}
	if !cfg.credsSet {
		cfg.creds = aws.CredentialsFromEnv()
	}
	if !cfg.creds.Valid() {
		return nil, ErrNoCredentials
	}
	if cfg.region == "" {
		return nil, ErrNoRegion
	}
	if cfg.endpoint == "" {
		cfg.endpoint = "https://dynamodb." + cfg.region + ".amazonaws.com"
	}
	if !hasScheme(cfg.endpoint) {
		cfg.endpoint = "https://" + cfg.endpoint
	}
	endpoint, err := url.Parse(cfg.endpoint)
	if err != nil {
		return nil, err
	}
	if endpoint.Host == "" {
		return nil, &url.Error{Op: "parse", URL: cfg.endpoint, Err: errNoHost}
	}
	// Every operation posts to the root, whatever path the endpoint carried.
	endpoint.Path = "/"
	endpoint.RawPath = ""
	endpoint.RawQuery = ""

	client := cfg.httpClient
	ownsHTTP := client == nil
	if ownsHTTP {
		timeout := cfg.timeout
		if timeout == 0 {
			timeout = DefaultTimeout
		}
		client = aws.NewHTTPClient(aws.ClientOptions{
			Timeout:             timeout,
			MaxIdleConnsPerHost: cfg.maxIdleConns,
		})
	}
	base := cfg.retryBase
	if base <= 0 {
		base = DefaultRetryBase
	}

	return &Client{
		endpoint: endpoint,
		region:   cfg.region,
		creds:    cfg.creds,
		http:     client,
		ownsHTTP: ownsHTTP,
		attempts: cfg.attempts,
		base:     base,
		now:      time.Now,
		sleep:    sleep,
	}, nil
}

// Close releases the pooled connections held for the endpoint.
//
// It matters more than it looks: a pooled TLS connection holds a handle in the
// TLS stack of the host OS, which outlives the last request until the idle
// timeout expires. A client supplied through WithHTTPClient is left alone,
// since its owner may still be using it.
func (c *Client) Close() error {
	if c.ownsHTTP {
		aws.CloseIdleConnections(c.http)
	}
	return nil
}

// Region reports the signing region.
func (c *Client) Region() string { return c.region }

// Endpoint reports the endpoint URL in use.
func (c *Client) Endpoint() string { return c.endpoint.String() }

func hasScheme(endpoint string) bool {
	for i := 0; i+2 < len(endpoint); i++ {
		if endpoint[i] == ':' && endpoint[i+1] == '/' && endpoint[i+2] == '/' {
			return true
		}
	}
	return false
}

// do sends one operation and decodes the reply into out, which may be nil.
//
// Retrying happens here rather than in each operation, so every call gets the
// same policy, and the request is rebuilt per attempt because signing is
// time-bound.
func (c *Client) do(ctx context.Context, op, table string, in, out any) error {
	payload, err := json.Marshal(in)
	if err != nil {
		return err
	}
	hash := aws.SHA256Hex(payload)

	attempts := c.attempts
	if attempts < 1 {
		attempts = 1
	}

	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			if err := c.sleep(ctx, c.backoff(attempt)); err != nil {
				return err
			}
		}

		body, retryable, err := c.send(ctx, op, table, payload, hash)
		if err != nil {
			lastErr = err
			if !retryable {
				return err
			}
			continue
		}
		if out == nil {
			return nil
		}
		if err := json.Unmarshal(body, out); err != nil {
			return &Error{Op: op, Table: table, StatusCode: http.StatusOK,
				Message: "cannot decode reply: " + err.Error(), err: err}
		}
		return nil
	}
	return lastErr
}

// send performs one attempt. It returns the reply body, and whether a failure
// is worth another attempt.
func (c *Client) send(ctx context.Context, op, table string, payload []byte, hash string) (body []byte, retryable bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint.String(), bytes.NewReader(payload))
	if err != nil {
		return nil, false, err
	}
	req.ContentLength = int64(len(payload))
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("X-Amz-Target", apiVersion+op)
	// Ask for no encoding: the checksum below covers the bytes as sent, and
	// setting the header also stops net/http from adding transparent gzip.
	req.Header.Set("Accept-Encoding", "identity")

	aws.Sign(req, c.creds, aws.SignRequest{
		Service:     "dynamodb",
		Region:      c.region,
		PayloadHash: hash,
		Time:        c.now(),
	})

	resp, err := c.http.Do(req)
	if err != nil {
		// A transport failure is worth one more attempt: the connection may
		// have been closed by the peer while it sat idle.
		return nil, true, err
	}
	defer resp.Body.Close()

	body, err = io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return nil, true, err
	}
	requestID := resp.Header.Get("X-Amzn-Requestid")

	if err := verifyChecksum(resp.Header.Get("X-Amz-Crc32"), body); err != nil {
		return nil, true, &Error{Op: op, Table: table, StatusCode: resp.StatusCode,
			Message: err.Error(), RequestID: requestID, err: ErrChecksumMismatch}
	}

	if resp.StatusCode != http.StatusOK {
		e := newError(op, table, resp.StatusCode, body, requestID)
		return nil, e.Retryable(), e
	}
	return body, false, nil
}

// verifyChecksum compares the body against the x-amz-crc32 header.
//
// An absent header is accepted rather than treated as corruption: DynamoDB and
// DynamoDB Local both send it, but a proxy in between is free to drop it, and
// failing every request in that case would be a worse answer than not checking.
func verifyChecksum(header string, body []byte) error {
	if header == "" {
		return nil
	}
	want, err := strconv.ParseUint(header, 10, 32)
	if err != nil {
		return nil // not a checksum this package understands; not a corruption signal
	}
	if got := crc32.ChecksumIEEE(body); uint64(got) != want {
		return errChecksum{want: uint32(want), got: got}
	}
	return nil
}

type errChecksum struct{ want, got uint32 }

func (e errChecksum) Error() string {
	return "checksum " + strconv.FormatUint(uint64(e.got), 10) +
		" does not match x-amz-crc32 " + strconv.FormatUint(uint64(e.want), 10)
}

// backoff returns the delay before the given attempt, exponential with full
// jitter so a fleet of clients retrying a throttled table does not synchronize.
func (c *Client) backoff(attempt int) time.Duration {
	d := c.base << (attempt - 1)
	if d > retryCap || d <= 0 {
		d = retryCap
	}
	return time.Duration(jitter()%uint64(d)) + d/2
}

// jitterState carries a small xorshift generator. Backoff jitter needs spread,
// not statistical quality, and math/rand would be this package's only reason
// to link it.
var jitterState atomic.Uint64

func jitter() uint64 {
	for {
		old := jitterState.Load()
		s := old
		if s == 0 {
			s = uint64(time.Now().UnixNano()) | 1
		}
		s ^= s << 13
		s ^= s >> 7
		s ^= s << 17
		if jitterState.CompareAndSwap(old, s) {
			return s
		}
	}
}

// sleep waits, or returns early when the context ends. A cancelled context ends
// retrying immediately: the caller's deadline outranks the retry budget.
func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
