package datastore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/shibukawa/tinygodriver/cloud/google"
	"github.com/shibukawa/tinygodriver/internal/cloudhttp"
)

// Defaults. The timeout matches the DynamoDB client because the workload is the
// same shape: one host, many small round trips.
const (
	defaultEndpoint    = "https://datastore.googleapis.com"
	defaultAudience    = "https://datastore.googleapis.com/"
	defaultTimeout     = 10 * time.Second
	defaultMaxIdle     = 4
	defaultAttempts    = 3
	defaultBackoffBase = 25 * time.Millisecond
	backoffCap         = time.Second
	maxResponseBytes   = 16 << 20 // above the 10 MiB request limit, below anything runaway
)

// Configuration errors.
var (
	ErrNoProject           = errors.New("datastore: no project configured")
	ErrNoCredentials       = errors.New("datastore: no credentials configured")
	ErrHTTPClientOwnership = errors.New("datastore: WithHTTPClient cannot be combined with WithMaxIdleConns")
)

// Client talks to one Datastore endpoint.
//
// It is safe for concurrent use. Close releases pooled connections and the
// cached token; a client dropped without Close leaves native TLS handles alive
// until they idle out, which is the whole reason this repository exists.
type Client struct {
	projectID string
	database  string
	namespace string
	endpoint  string

	tokens     google.TokenSource
	cache      *google.CachedSource
	signer     io.Closer
	httpClient *http.Client
	ownsClient bool
	timeout    time.Duration

	attempts    int
	backoffBase time.Duration

	// randFloat produces the jitter factor. A field so tests can make backoff
	// deterministic.
	randFloat func() float64
}

// New builds a client for one project.
//
// Credentials resolve in this order: an explicit token source, explicit
// credentials, the emulator (which needs none), then
// GOOGLE_APPLICATION_CREDENTIALS.
func New(projectID string, opts ...Option) (*Client, error) {
	cfg := config{
		timeout:     defaultTimeout,
		maxIdle:     defaultMaxIdle,
		attempts:    defaultAttempts,
		backoffBase: defaultBackoffBase,
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	if projectID == "" {
		projectID = google.ProjectIDFromEnv()
	}
	if projectID == "" {
		return nil, ErrNoProject
	}

	emulator := google.EmulatorHost("datastore")
	endpoint := cfg.endpoint
	if endpoint == "" {
		endpoint = emulator
	}
	if endpoint == "" {
		endpoint = defaultEndpoint
	}
	endpoint = normalizeEndpoint(endpoint)

	c := &Client{
		projectID:   projectID,
		database:    cfg.database,
		namespace:   cfg.namespace,
		endpoint:    endpoint,
		attempts:    cfg.attempts,
		backoffBase: cfg.backoffBase,
		timeout:     cfg.timeout,
		randFloat:   rand.Float64,
	}

	if cfg.httpClient != nil {
		if cfg.maxIdleSet {
			return nil, ErrHTTPClientOwnership
		}
		c.httpClient = cfg.httpClient
	} else {
		c.httpClient = google.NewHTTPClient(google.ClientOptions{
			Timeout:             0,
			MaxIdleConnsPerHost: cfg.maxIdle,
		})
		c.ownsClient = true
	}

	if err := c.resolveTokens(cfg, emulator != "" && cfg.endpoint == ""); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Client) resolveTokens(cfg config, usingEmulator bool) error {
	switch {
	case cfg.tokenSource != nil:
		c.tokens = cfg.tokenSource
		return nil
	case usingEmulator:
		// The emulator ignores Authorization entirely. Sending nothing is
		// honest about that, rather than minting a token it will not read.
		return nil
	}

	creds := cfg.credentials
	if creds == nil {
		fromEnv, err := google.CredentialsFromEnv()
		if err != nil {
			return ErrNoCredentials
		}
		creds = &fromEnv
	}
	source, err := google.JWTTokenSource(*creds, defaultAudience)
	if err != nil {
		return err
	}
	c.signer = source
	c.cache = google.Cached(source)
	c.tokens = c.cache
	return nil
}

func normalizeEndpoint(endpoint string) string {
	// DATASTORE_EMULATOR_HOST carries a bare host:port. Emulators speak plain
	// HTTP, so that is the scheme a schemeless value gets.
	if !strings.Contains(endpoint, "://") {
		endpoint = "http://" + endpoint
	}
	return strings.TrimSuffix(endpoint, "/")
}

// ProjectID is the project this client addresses.
func (c *Client) ProjectID() string { return c.projectID }

// Endpoint is the host this client sends to.
func (c *Client) Endpoint() string { return c.endpoint }

// Namespace is the default namespace for keys that carry none.
func (c *Client) Namespace() string { return c.namespace }

// Close releases pooled connections and the signing key.
//
// A client built with WithHTTPClient leaves that client alone, since its owner
// may still be using it.
func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	if c.ownsClient {
		google.CloseIdleConnections(c.httpClient)
	}
	if c.cache != nil {
		c.cache.Invalidate()
	}
	if c.signer != nil {
		return c.signer.Close()
	}
	return nil
}

func (c *Client) partition() *wirePartitionID {
	p := &wirePartitionID{ProjectID: c.projectID, DatabaseID: c.database}
	if c.namespace != "" {
		p.NamespaceID = c.namespace
	}
	return p
}

// encodeKey renders a key with the project and database attached, which is what
// makes a Key portable inside a program: it carries only what identifies the
// entity, and the client supplies the rest.
func (c *Client) encodeKey(k Key) wireKey {
	partition := c.partition()
	if k.Namespace != "" {
		withNamespace := *partition
		withNamespace.NamespaceID = k.Namespace
		partition = &withNamespace
	}
	return k.wire(partition)
}

func (c *Client) encodeEntity(e Entity) (json.RawMessage, error) {
	// The properties go through encodeProperties rather than being marshalled
	// directly, because a keyValue property needs the partition too. Marshalling
	// the map would emit a key with no project, which is a stored reference
	// naming nothing.
	properties, err := encodeProperties(e.Properties, c.partition())
	if err != nil {
		return nil, err
	}
	out := struct {
		Key        *wireKey                   `json:"key,omitempty"`
		Properties map[string]json.RawMessage `json:"properties,omitempty"`
	}{Properties: properties}
	if e.Key != nil {
		key := c.encodeKey(*e.Key)
		out.Key = &key
	}
	return json.Marshal(out)
}

// call performs one RPC with retries.
//
// The operation is in the URL path rather than a header, which is the main
// shape difference from the DynamoDB client.
func (c *Client) call(ctx context.Context, op, kind string, request, response any) error {
	ctx, cancel := cloudhttp.OperationContext(ctx, c.timeout)
	defer cancel()

	body, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("datastore: encoding %s: %w", op, err)
	}

	endpoint := c.endpoint + "/v1/projects/" + url.PathEscape(c.projectID) + ":" + op

	attempts := c.attempts
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	internalRetried := false

	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			if err := c.sleep(ctx, attempt); err != nil {
				return err
			}
		}
		raw, httpStatus, err := c.roundTrip(ctx, endpoint, body)
		if err != nil {
			// A transport failure is retried here, which is what makes the
			// TinyGo and std Go paths behave alike: the native transport
			// replays a dead pooled connection and net/http declines to.
			lastErr = err
			continue
		}
		if httpStatus == http.StatusOK {
			if response == nil {
				return nil
			}
			if err := json.Unmarshal(raw, response); err != nil {
				return fmt.Errorf("datastore: decoding %s: %w", op, err)
			}
			return nil
		}

		serviceErr := parseError(op, kind, httpStatus, raw)
		lastErr = serviceErr

		if serviceErr.Status == "UNAUTHENTICATED" {
			// A token this client believed was current was rejected, which on a
			// device in the field usually means the clock, not the key. Refresh
			// once and resend; the request was not at fault, so this does not
			// consume the retry budget.
			if c.cache != nil && !internalRetried {
				internalRetried = true
				c.cache.Invalidate()
				attempt--
				continue
			}
			return serviceErr
		}
		if serviceErr.Status == "INTERNAL" {
			// Documented as "do not retry more than once".
			if internalRetried {
				return serviceErr
			}
			internalRetried = true
			continue
		}
		if !serviceErr.Retryable() {
			return serviceErr
		}
	}
	return lastErr
}

func (c *Client) roundTrip(ctx context.Context, endpoint string, body []byte) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	// gzip is not requested: the TinyGo http client does not transparently
	// decompress and these payloads are small.
	req.Header.Set("Accept-Encoding", "identity")

	if c.tokens != nil {
		token, err := c.tokens.Token(ctx)
		if err != nil {
			return nil, 0, err
		}
		req.Header.Set("Authorization", "Bearer "+token.Value)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	// Reading to EOF is what makes the connection reusable; an abandoned reply
	// costs the next call a handshake.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return raw, resp.StatusCode, nil
}

// sleep waits out the backoff for the given attempt, or returns the context
// error if that comes first.
func (c *Client) sleep(ctx context.Context, attempt int) error {
	delay := c.backoffBase
	if delay <= 0 {
		delay = defaultBackoffBase
	}
	for i := 1; i < attempt && delay < backoffCap; i++ {
		delay *= 2
	}
	if delay > backoffCap {
		delay = backoffCap
	}
	// Full jitter: anywhere in [0, delay].
	jittered := time.Duration(c.randFloat() * float64(delay))

	timer := time.NewTimer(jittered)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func parseInt64(s string) int64 {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return n
}
