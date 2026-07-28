package s3

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Internal failures that are not S3 error documents.
var (
	errNoHost                = errors.New("s3: endpoint has no host")
	errRedirectWithoutTarget = errors.New("s3: redirect without Location or x-amz-bucket-region")
	errStreamNotReplayable   = errors.New("s3: redirected request cannot replay a streamed body")
)

// DefaultTimeout bounds a single request when no timeout is configured.
const DefaultTimeout = 60 * time.Second

// maxRedirects bounds redirect following. S3 needs one hop to correct a region
// or a bucket location; more than a few means a misconfigured endpoint.
const maxRedirects = 3

// Client talks to one S3 endpoint. It is safe for concurrent use.
type Client struct {
	endpoint  *url.URL
	creds     Credentials
	pathStyle bool
	unsigned  bool
	http      *http.Client
	now       func() time.Time // overridden in tests

	// mu guards region, which a redirect updates when the bucket turns out to
	// live somewhere else.
	mu     sync.RWMutex
	region string
}

type config struct {
	endpoint   string
	region     string
	creds      Credentials
	credsSet   bool
	pathStyle  *bool
	unsigned   bool
	timeout    time.Duration
	httpClient *http.Client
}

// Option configures a Client.
type Option func(*config)

// WithEndpoint overrides the endpoint URL, for S3-compatible servers such as
// RustFS or MinIO. It defaults to AWS_ENDPOINT_URL_S3, AWS_ENDPOINT_URL, or the
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
func WithCredentials(creds Credentials) Option {
	return func(c *config) {
		c.creds = creds
		c.credsSet = true
	}
}

// WithCredentialsFromEnv reads credentials from the environment. New does this
// already when no credentials option is given; the option states it explicitly.
func WithCredentialsFromEnv() Option {
	return func(c *config) {
		c.creds = CredentialsFromEnv()
		c.credsSet = true
	}
}

// WithPathStyle selects between https://endpoint/bucket/key and
// https://bucket.endpoint/key addressing.
//
// The default is virtual-host addressing for amazonaws.com endpoints and path
// addressing everywhere else, which is what S3-compatible servers expect.
func WithPathStyle(pathStyle bool) Option {
	return func(c *config) { c.pathStyle = &pathStyle }
}

// WithUnsignedPayload signs requests with UNSIGNED-PAYLOAD instead of the body
// hash, so Put streams a body it cannot rewind instead of buffering it.
//
// The request headers stay signed. Use it only over https, where TLS protects
// the body that the signature no longer covers.
func WithUnsignedPayload(unsigned bool) Option {
	return func(c *config) { c.unsigned = unsigned }
}

// WithTimeout bounds each request, including reading the response body. Zero
// means DefaultTimeout.
func WithTimeout(timeout time.Duration) Option {
	return func(c *config) { c.timeout = timeout }
}

// WithHTTPClient supplies the http.Client to use.
//
// Its CheckRedirect must return http.ErrUseLastResponse on standard Go builds:
// a redirected request has to be signed again for its new host, which this
// package does itself.
func WithHTTPClient(client *http.Client) Option {
	return func(c *config) { c.httpClient = client }
}

// New builds a Client. Region, endpoint, and credentials fall back to the
// environment, so a configured shell needs no options at all.
func New(opts ...Option) (*Client, error) {
	cfg := &config{
		endpoint: endpointFromEnv(),
		region:   regionFromEnv(),
	}
	for _, opt := range opts {
		opt(cfg)
	}
	if !cfg.credsSet {
		cfg.creds = CredentialsFromEnv()
	}
	if !cfg.creds.valid() {
		return nil, ErrNoCredentials
	}
	if cfg.region == "" {
		return nil, ErrNoRegion
	}
	if cfg.endpoint == "" {
		cfg.endpoint = "https://s3." + cfg.region + ".amazonaws.com"
	}
	if !strings.Contains(cfg.endpoint, "://") {
		cfg.endpoint = "https://" + cfg.endpoint
	}
	endpoint, err := url.Parse(cfg.endpoint)
	if err != nil {
		return nil, err
	}
	if endpoint.Host == "" {
		return nil, &url.Error{Op: "parse", URL: cfg.endpoint, Err: errNoHost}
	}

	pathStyle := !isAWSEndpoint(endpoint.Host)
	if cfg.pathStyle != nil {
		pathStyle = *cfg.pathStyle
	}
	httpClient := cfg.httpClient
	if httpClient == nil {
		timeout := cfg.timeout
		if timeout == 0 {
			timeout = DefaultTimeout
		}
		httpClient = newHTTPClient(timeout)
	}

	return &Client{
		endpoint:  endpoint,
		region:    cfg.region,
		creds:     cfg.creds,
		pathStyle: pathStyle,
		unsigned:  cfg.unsigned,
		http:      httpClient,
		now:       time.Now,
	}, nil
}

// Region reports the signing region, which redirect handling may have updated.
func (c *Client) Region() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.region
}

func (c *Client) setRegion(region string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.region = region
}

// Endpoint reports the endpoint URL in use.
func (c *Client) Endpoint() string { return c.endpoint.String() }

func isAWSEndpoint(host string) bool {
	if i := strings.LastIndex(host, ":"); i != -1 {
		host = host[:i]
	}
	return strings.HasSuffix(host, ".amazonaws.com") || host == "amazonaws.com"
}

// request is one S3 call, before it becomes an *http.Request.
type request struct {
	op     string
	method string
	bucket string
	key    string
	params [][2]string
	header map[string]string

	// body returns a fresh reader each time the request is sent, so a redirect
	// can be retried. It is nil for bodyless requests.
	body func() (io.ReadCloser, error)

	// length is the body size, or -1 when unknown.
	length int64

	// payloadHash is the value signed as x-amz-content-sha256.
	payloadHash string
}

// buildURL renders the request URL. The escaped path and query are stored on
// the URL so the signature covers exactly what goes on the wire.
func (c *Client) buildURL(r *request) *url.URL {
	u := *c.endpoint
	host := u.Host
	path := "/"

	switch {
	case r.bucket == "":
	case c.pathStyle:
		path = "/" + r.bucket
		if r.key != "" {
			path += "/" + r.key
		}
	default:
		host = r.bucket + "." + host
		if r.key != "" {
			path = "/" + r.key
		}
	}

	u.Host = host
	u.Path = path
	u.RawPath = uriEncode(path, false)
	u.RawQuery = canonicalQuery(r.params)
	return &u
}

// do sends r, following redirects itself so each hop is signed again.
//
// The returned response has an unread body on success; the caller closes it.
func (c *Client) do(ctx context.Context, r *request) (*http.Response, error) {
	target := c.buildURL(r)
	region := c.Region()

	for redirects := 0; ; redirects++ {
		var body io.ReadCloser
		if r.body != nil {
			var err error
			if body, err = r.body(); err != nil {
				return nil, err
			}
		}

		req, err := http.NewRequestWithContext(ctx, r.method, target.String(), body)
		if err != nil {
			return nil, err
		}
		// NewRequestWithContext re-parses the URL, which drops the AWS-style
		// escaping. Restore it, together with the exact body length.
		req.URL = target
		req.Host = target.Host
		req.ContentLength = r.length
		for name, value := range r.header {
			req.Header.Set(name, value)
		}
		sign(req, c.creds, region, r.payloadHash, c.now())

		resp, err := c.http.Do(req)
		if err != nil {
			return nil, err
		}
		if !isRedirect(resp.StatusCode) {
			return resp, nil
		}

		next, newRegion, err := redirectTarget(resp, target)
		drain(resp)
		if err != nil {
			return nil, &Error{Op: r.op, Bucket: r.bucket, Key: r.key,
				StatusCode: resp.StatusCode, Code: "Redirect", Message: err.Error(), err: err}
		}
		if redirects >= maxRedirects {
			return nil, &Error{Op: r.op, Bucket: r.bucket, Key: r.key,
				StatusCode: resp.StatusCode, Code: "Redirect", err: ErrTooManyRedirect}
		}
		if newRegion != "" && newRegion != region {
			region = newRegion
			c.setRegion(newRegion)
		}
		target = next
	}
}

func isRedirect(status int) bool {
	switch status {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther,
		http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return true
	}
	return false
}

// redirectTarget resolves where a redirect points. S3 answers a region mismatch
// with x-amz-bucket-region and, for a plain GET, a Location header.
func redirectTarget(resp *http.Response, current *url.URL) (*url.URL, string, error) {
	region := resp.Header.Get("X-Amz-Bucket-Region")

	if loc := resp.Header.Get("Location"); loc != "" {
		next, err := url.Parse(loc)
		if err != nil {
			return nil, "", err
		}
		next = current.ResolveReference(next)
		// Keep the AWS-style escaping across the hop.
		next.RawPath = uriEncode(next.Path, false)
		return next, region, nil
	}

	if region == "" {
		return nil, "", errRedirectWithoutTarget
	}
	// Region-only redirect: the regional endpoint is derived from the region.
	next := *current
	if host, ok := retargetRegion(next.Host, region); ok {
		next.Host = host
	}
	return &next, region, nil
}

// retargetRegion rewrites the region label of an AWS endpoint host, so
// s3.us-east-1.amazonaws.com becomes s3.eu-west-1.amazonaws.com. The legacy
// global host s3.amazonaws.com gains a region label instead.
func retargetRegion(host, region string) (string, bool) {
	if !isAWSEndpoint(host) {
		return "", false
	}
	labels := strings.Split(host, ".")
	for i, label := range labels {
		if label != "s3" || i+1 >= len(labels) {
			continue
		}
		if labels[i+1] == "amazonaws" {
			rest := append([]string{region}, labels[i+1:]...)
			return strings.Join(append(labels[:i+1:i+1], rest...), "."), true
		}
		labels[i+1] = region
		return strings.Join(labels, "."), true
	}
	return "", false
}

// drain consumes and closes a body so the response can be discarded.
func drain(resp *http.Response) {
	if resp.Body == nil {
		return
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	resp.Body.Close()
}

// readBody reads and closes a response body, bounded so a hostile or confused
// endpoint cannot exhaust memory on an error path.
func readBody(resp *http.Response, limit int64) ([]byte, error) {
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

// maxErrorBody bounds an error document.
const maxErrorBody = 1 << 20

// errorFrom turns a failed response into an *Error, consuming the body.
func (c *Client) errorFrom(r *request, resp *http.Response) error {
	body, _ := readBody(resp, maxErrorBody)
	return newError(r.op, r.bucket, r.key, resp.StatusCode, body, resp.Header.Get("X-Amz-Request-Id"))
}

// bodyFromReader prepares a request body: it determines the length and the
// payload hash, rewinding or buffering as needed.
//
// A seekable reader is hashed in place. Anything else is buffered, unless the
// client signs with UNSIGNED-PAYLOAD, in which case it streams. known is an
// explicit content length, or nil.
func (c *Client) bodyFromReader(r io.Reader, known *int64) (get func() (io.ReadCloser, error), length int64, hash string, err error) {
	if r == nil {
		return nil, 0, emptyPayloadHash, nil
	}

	if c.unsigned {
		switch seeker, ok := r.(io.Seeker); {
		case known != nil:
			return onceReader(r), *known, unsignedPayload, nil
		case ok:
			if n, serr := seekableLength(seeker); serr == nil {
				return onceReader(r), n, unsignedPayload, nil
			}
		}
		// An unknown length means chunked transfer encoding, which AWS rejects
		// for PutObject. WithContentLength is the way out.
		return onceReader(r), -1, unsignedPayload, nil
	}

	if seeker, ok := r.(io.Seeker); ok {
		start, serr := seeker.Seek(0, io.SeekCurrent)
		if serr == nil {
			sum, n, herr := hashStream(r)
			if herr != nil {
				return nil, 0, "", herr
			}
			if _, serr = seeker.Seek(start, io.SeekStart); serr == nil {
				get = func() (io.ReadCloser, error) {
					if _, err := seeker.Seek(start, io.SeekStart); err != nil {
						return nil, err
					}
					return io.NopCloser(r), nil
				}
				return get, n, sum, nil
			}
		}
	}

	buf, rerr := io.ReadAll(r)
	if rerr != nil {
		return nil, 0, "", rerr
	}
	return func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(buf)), nil
	}, int64(len(buf)), sha256Hex(buf), nil
}

// onceReader hands the reader over for a single send. A redirect cannot replay
// it, and reports that instead of sending a truncated body.
func onceReader(r io.Reader) func() (io.ReadCloser, error) {
	used := false
	return func() (io.ReadCloser, error) {
		if used {
			return nil, errStreamNotReplayable
		}
		used = true
		return io.NopCloser(r), nil
	}
}

func seekableLength(s io.Seeker) (int64, error) {
	current, err := s.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0, err
	}
	end, err := s.Seek(0, io.SeekEnd)
	if err != nil {
		return 0, err
	}
	if _, err := s.Seek(current, io.SeekStart); err != nil {
		return 0, err
	}
	return end - current, nil
}

// hashStream reads r to the end, returning the hex SHA-256 and the byte count.
func hashStream(r io.Reader) (string, int64, error) {
	h := sha256.New()
	n, err := io.Copy(h, r)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}
