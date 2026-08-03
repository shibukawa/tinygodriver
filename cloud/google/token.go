package google

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/shibukawa/tinygodriver/jwt"
)

const (
	defaultTokenURI  = "https://oauth2.googleapis.com/token"
	metadataTokenURL = "http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token"

	// tokenLifetime is what the self-signed JWT claims. An hour is the maximum
	// Google accepts and there is nothing to gain from a shorter one: the token
	// never leaves this process except to the service it names.
	tokenLifetime = time.Hour

	// refreshMargin is how far before expiry a cached token is replaced.
	// Generous because the cost of being early is one 3 ms signature and the
	// cost of being late is a request failing with UNAUTHENTICATED.
	refreshMargin = 60 * time.Second

	maxTokenResponseBytes = 1 << 20
)

// Token is a bearer credential and the instant it stops being one.
type Token struct {
	Value  string
	Expiry time.Time
}

// Valid reports whether t is usable at the given time, allowing for the
// refresh margin.
func (t Token) Valid(now time.Time) bool {
	return t.Value != "" && now.Add(refreshMargin).Before(t.Expiry)
}

// TokenSource produces bearer tokens.
type TokenSource interface {
	Token(ctx context.Context) (Token, error)
}

// StaticTokenSource returns a source that always yields t.
//
// This is the escape hatch for a device provisioned by a companion service, and
// the only path that links no signing code at all: a binary using it contains
// neither crypto/rsa nor the native RSA backend.
func StaticTokenSource(t Token) TokenSource { return staticSource{t} }

type staticSource struct{ token Token }

func (s staticSource) Token(context.Context) (Token, error) {
	if s.token.Value == "" {
		return Token{}, ErrNoCredentials
	}
	return s.token, nil
}

// JWTSource mints self-signed JWTs, the default credential path.
//
// There is no token endpoint on this path: the JWT is the bearer value. That
// removes a second host from the connection pool and a round trip from the
// first call, which is the whole reason it is the default.
type JWTSource struct {
	signer   *RSASigner
	email    string
	keyID    string
	audience string

	// Now is the clock. Nil means time.Now. A self-signed JWT is only valid
	// against the server's clock, so this is the one knob that matters when a
	// device disagrees with Google about what time it is.
	Now func() time.Time
}

// JWTTokenSource builds a self-signed JWT source for one audience.
//
// The audience is the service host with a trailing slash, for example
// "https://datastore.googleapis.com/". It is per-service, so a second Google
// service needs its own source; a token minted for one audience is not accepted
// by another.
func JWTTokenSource(c Credentials, audience string) (*JWTSource, error) {
	if audience == "" {
		return nil, fmt.Errorf("google: audience is required for a self-signed JWT")
	}
	signer, err := NewRSASigner(c)
	if err != nil {
		return nil, err
	}
	return &JWTSource{
		signer:   signer,
		email:    c.ClientEmail,
		keyID:    c.PrivateKeyID,
		audience: audience,
	}, nil
}

func (s *JWTSource) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Token mints a JWT. It signs on every call; wrap with Cached to sign once per
// token lifetime instead.
func (s *JWTSource) Token(ctx context.Context) (Token, error) {
	if s == nil || s.signer == nil {
		return Token{}, ErrNoCredentials
	}
	if err := ctx.Err(); err != nil {
		return Token{}, err
	}
	issued := s.now()
	expiry := issued.Add(tokenLifetime)
	issuedAt, expiresAt := issued.Unix(), expiry.Unix()

	// aud and scope are mutually exclusive on this path; only aud is set.
	compact, err := jwt.Sign(
		jwt.Header{Type: "JWT", KeyID: s.keyID},
		jwt.Claims{
			Issuer:    s.email,
			Subject:   s.email,
			Audience:  []string{s.audience},
			IssuedAt:  &issuedAt,
			ExpiresAt: &expiresAt,
		},
		s.signer,
	)
	if err != nil {
		return Token{}, err
	}
	return Token{Value: compact, Expiry: expiry}, nil
}

// Close releases the signing key.
func (s *JWTSource) Close() error {
	if s == nil {
		return nil
	}
	return s.signer.Close()
}

// OAuth2Source exchanges a signed assertion for an access token at the token
// endpoint.
//
// It costs a second host in the connection pool and a round trip before the
// first real call. JWTSource avoids both, so this exists for deployments that
// require a real access token rather than as the ordinary path.
type OAuth2Source struct {
	signer   *RSASigner
	email    string
	keyID    string
	scope    string
	tokenURI string

	// HTTPClient is the client used for the exchange. Nil means
	// http.DefaultClient.
	HTTPClient *http.Client

	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

// OAuth2TokenSource builds an access-token source for the given scopes.
func OAuth2TokenSource(c Credentials, scopes ...string) (*OAuth2Source, error) {
	if len(scopes) == 0 {
		return nil, fmt.Errorf("google: at least one scope is required for a token exchange")
	}
	signer, err := NewRSASigner(c)
	if err != nil {
		return nil, err
	}
	uri := c.TokenURI
	if uri == "" {
		uri = defaultTokenURI
	}
	return &OAuth2Source{
		signer:   signer,
		email:    c.ClientEmail,
		keyID:    c.PrivateKeyID,
		scope:    strings.Join(scopes, " "),
		tokenURI: uri,
	}, nil
}

func (s *OAuth2Source) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Token signs an assertion and exchanges it.
func (s *OAuth2Source) Token(ctx context.Context) (Token, error) {
	if s == nil || s.signer == nil {
		return Token{}, ErrNoCredentials
	}
	issued := s.now()
	issuedAt, expiresAt := issued.Unix(), issued.Add(tokenLifetime).Unix()

	// Here the audience is the token endpoint, not the service, and the
	// requested scope rides along. That is the difference from JWTSource.
	assertion, err := jwt.Sign(
		jwt.Header{Type: "JWT", KeyID: s.keyID},
		jwt.Claims{
			Issuer:    s.email,
			Audience:  []string{s.tokenURI},
			IssuedAt:  &issuedAt,
			ExpiresAt: &expiresAt,
			Raw:       map[string]json.RawMessage{"scope": mustJSONString(s.scope)},
		},
		s.signer,
	)
	if err != nil {
		return Token{}, err
	}

	form := url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"assertion":  {assertion},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.tokenURI,
		strings.NewReader(form.Encode()))
	if err != nil {
		return Token{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return s.exchange(req, issued)
}

func (s *OAuth2Source) exchange(req *http.Request, issued time.Time) (Token, error) {
	client := s.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return Token{}, err
	}
	defer resp.Body.Close()
	// Read to EOF so the connection can return to the pool; an abandoned reply
	// costs the next call a handshake.
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxTokenResponseBytes))
	if err != nil {
		return Token{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return Token{}, fmt.Errorf("google: token endpoint returned %d: %s",
			resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return decodeTokenResponse(body, issued)
}

// MetadataSource reads a token from the GCE metadata server.
//
// It needs no key and links no signing code, which makes it the cheapest path
// by binary size. It only works where the metadata server exists: GCE, Cloud
// Run, GKE.
type MetadataSource struct {
	// HTTPClient is the client used for the lookup. Nil means
	// http.DefaultClient.
	HTTPClient *http.Client

	// URL overrides the metadata endpoint. Empty means the standard one.
	URL string

	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

// MetadataTokenSource returns a source backed by the metadata server.
func MetadataTokenSource() *MetadataSource { return &MetadataSource{} }

func (s *MetadataSource) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Token fetches an access token.
func (s *MetadataSource) Token(ctx context.Context) (Token, error) {
	endpoint := s.URL
	if endpoint == "" {
		endpoint = metadataTokenURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Token{}, err
	}
	// The metadata server refuses a request without this header, which is what
	// stops a confused deputy from being talked into fetching a token.
	req.Header.Set("Metadata-Flavor", "Google")

	client := s.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	issued := s.now()
	resp, err := client.Do(req)
	if err != nil {
		return Token{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxTokenResponseBytes))
	if err != nil {
		return Token{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return Token{}, fmt.Errorf("google: metadata server returned %d", resp.StatusCode)
	}
	return decodeTokenResponse(body, issued)
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int64  `json:"expires_in"`
}

func decodeTokenResponse(body []byte, issued time.Time) (Token, error) {
	var r tokenResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return Token{}, fmt.Errorf("google: malformed token response: %w", err)
	}
	if r.AccessToken == "" {
		return Token{}, errors.New("google: token response carried no access_token")
	}
	if r.ExpiresIn <= 0 {
		return Token{}, errors.New("google: token response carried no lifetime")
	}
	return Token{
		Value:  r.AccessToken,
		Expiry: issued.Add(time.Duration(r.ExpiresIn) * time.Second),
	}, nil
}

// CachedSource holds one token and replaces it shortly before it expires.
type CachedSource struct {
	source TokenSource

	// Now is the clock; nil means time.Now.
	Now func() time.Time

	mu    sync.Mutex
	token Token
}

// Cached wraps a source so a token is fetched once per lifetime rather than
// once per request.
//
// The refresh happens on the calling goroutine, under a mutex: one refresh is
// in flight and the others wait for it. There is no background refresher, both
// because it would outlive the client unless something stopped it and because
// TinyGo goroutines are not OS threads, so a goroutine cannot be relied on to
// make progress while another blocks in a socket call.
func Cached(source TokenSource) *CachedSource {
	return &CachedSource{source: source}
}

func (c *CachedSource) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// Token returns the cached token, refreshing it if it is missing or nearly
// expired.
func (c *CachedSource) Token(ctx context.Context) (Token, error) {
	if c == nil || c.source == nil {
		return Token{}, ErrNoCredentials
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token.Valid(c.now()) {
		return c.token, nil
	}
	token, err := c.source.Token(ctx)
	if err != nil {
		return Token{}, err
	}
	c.token = token
	return token, nil
}

// Invalidate drops the cached token, so the next Token call fetches a new one.
//
// A service client calls this when the server rejects a token it believed was
// current, which happens when the two disagree about the time.
func (c *CachedSource) Invalidate() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.token = Token{}
	c.mu.Unlock()
}

func mustJSONString(s string) json.RawMessage {
	b, err := json.Marshal(s)
	if err != nil {
		// json.Marshal of a string fails only on invalid UTF-8, which a scope
		// never contains; the fallback keeps this total rather than panicking.
		return json.RawMessage(`""`)
	}
	return b
}
