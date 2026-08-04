package datastore

import (
	"net/http"
	"time"

	"github.com/shibukawa/tinygodriver/cloud/google"
)

// Option configures a Client.
type Option func(*config)

type config struct {
	endpoint    string
	database    string
	namespace   string
	credentials *google.Credentials
	tokenSource google.TokenSource
	timeout     time.Duration
	httpClient  *http.Client
	ownsClient  bool
	maxIdle     int
	attempts    int
	backoffBase time.Duration
}

// WithEndpoint overrides the service endpoint, which is how a client is pointed
// at the emulator. A value with no scheme is taken as http, matching what
// DATASTORE_EMULATOR_HOST carries.
func WithEndpoint(endpoint string) Option {
	return func(c *config) { c.endpoint = endpoint }
}

// WithDatabase selects a named database. Empty means the project's default.
func WithDatabase(database string) Option {
	return func(c *config) { c.database = database }
}

// WithNamespace sets the default namespace for keys that carry none.
func WithNamespace(namespace string) Option {
	return func(c *config) { c.namespace = namespace }
}

// WithCredentials supplies a service account key directly.
func WithCredentials(creds google.Credentials) Option {
	return func(c *config) { c.credentials = &creds }
}

// WithTokenSource supplies bearer tokens from somewhere else: a metadata
// server, a companion process, or a test.
//
// A client built this way never signs anything, so on a TinyGo build it links
// no RSA code at all.
func WithTokenSource(ts google.TokenSource) Option {
	return func(c *config) { c.tokenSource = ts }
}

// WithTimeout bounds one request including reading the response body. The
// default is 10s, matching the DynamoDB client: these are small round trips.
func WithTimeout(d time.Duration) Option {
	return func(c *config) { c.timeout = d }
}

// WithHTTPClient supplies the HTTP client. Close leaves such a client alone,
// since its owner may still be using it.
func WithHTTPClient(client *http.Client) Option {
	return func(c *config) { c.httpClient = client }
}

// WithMaxIdleConns sets how many idle connections are kept.
//
// Every request goes to one host, so the per-host cap is the whole pool for
// this client. Set it to the concurrency the application runs.
func WithMaxIdleConns(n int) Option {
	return func(c *config) { c.maxIdle = n }
}

// WithRetry configures the retry budget. Zero attempts disables retrying.
//
// Retries multiply: the TinyGo transport replays a request once below this
// client when a pooled connection dies before any response byte arrives, so the
// worst case is attempts x 2 deliveries. The mutation verbs are idempotent by
// construction, so that is harmless for them; a commit carrying a transaction
// handle fails on the second delivery rather than writing twice.
func WithRetry(attempts int, base time.Duration) Option {
	return func(c *config) {
		c.attempts = attempts
		c.backoffBase = base
	}
}

// ReadOption configures a read.
type ReadOption interface{ applyRead(*readConfig) }

// WriteOption configures a write.
type WriteOption interface{ applyWrite(*writeConfig) }

type readConfig struct {
	eventual bool
	readTime string
}

type writeConfig struct {
	baseVersion *int64
	updateTime  string
}

type eventualOption struct{}

func (eventualOption) applyRead(c *readConfig) { c.eventual = true }

// WithEventualConsistency asks for a possibly stale read.
//
// The default is strong, which on the Firestore backend applies to non-ancestor
// queries too. This exists for the cases where a stale answer is cheaper and
// good enough, not because staleness is ever the safer default.
func WithEventualConsistency() ReadOption { return eventualOption{} }

type readTimeOption struct{ at time.Time }

func (o readTimeOption) applyRead(c *readConfig) {
	c.readTime = o.at.UTC().Format(time.RFC3339Nano)
}

// WithReadTime reads the database as of a past instant.
//
// Two windows, and which one you are in changes what a legal instant is:
//
//   - Within the past hour, any microsecond-granularity instant, whether or not
//     point-in-time recovery is enabled on the database.
//   - From one hour to seven days back, whole-minute timestamps only, and only
//     with point-in-time recovery enabled.
//
// Neither window reaches before the database's earliestVersionTime, which on a
// young database can be later than both.
//
// A read older than an hour must therefore be truncated by the caller:
//
//	at := time.Now().Add(-2 * time.Hour).Truncate(time.Minute)
//
// Without that, the service refuses the read as "read_time is too old", which
// names the age when the precision is what was wrong. This does not truncate
// for you, because truncating would change the instant you asked for, and the
// boundary between the two windows moves while the request is in flight.
//
// None of this is checked here. The client cannot see whether PITR is enabled
// or what earliestVersionTime is, so a local range check would refuse reads
// that work.
func WithReadTime(at time.Time) ReadOption { return readTimeOption{at: at} }

type baseVersionOption struct{ version int64 }

func (o baseVersionOption) applyWrite(c *writeConfig) { c.baseVersion = &o.version }

// WithBaseVersion applies the write only if the stored entity is still at this
// version, which comes from a previous read.
//
// This is optimistic concurrency, and that is the honest name for it. It is
// strictly stronger than a client-side check, because it also catches a
// concurrent write the caller never read.
func WithBaseVersion(version int64) WriteOption { return baseVersionOption{version: version} }

type updateTimeOption struct{ at string }

func (o updateTimeOption) applyWrite(c *writeConfig) { c.updateTime = o.at }

// WithUpdateTime is WithBaseVersion keyed on a timestamp instead, taken from
// Entity.UpdateTime.
func WithUpdateTime(at string) WriteOption { return updateTimeOption{at: at} }
