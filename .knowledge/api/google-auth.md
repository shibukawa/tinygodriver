---
id: api:google-auth
type: api
title: Google Credentials And Token Sources
---
`cloud/google` holds what every Google Cloud client in this repository needs: credentials, bearer tokens, and the HTTP client the build selects. It is the counterpart of api:aws-signer, and it is shaped by the fact that Google wants a token rather than a signature.

```yaml
import_path: github.com/shibukawa/tinygodriver/cloud/google
state: proposed 2026-08-02
credentials: |
  type Credentials struct {
      Type        string   // "service_account"
      ProjectID   string
      ClientEmail string
      PrivateKey  string   // PKCS#8 PEM
      PrivateKeyID string  // JWT kid
      TokenURI    string
  }
  func CredentialsFromJSON(b []byte) (Credentials, error)
  func CredentialsFromFile(path string) (Credentials, error)
  func CredentialsFromEnv() (Credentials, error)   // GOOGLE_APPLICATION_CREDENTIALS
  func (c Credentials) Valid() bool
tokens: |
  type Token struct { Value string; Expiry time.Time }
  type TokenSource interface { Token(ctx context.Context) (Token, error) }

  func JWTTokenSource(c Credentials, audience string) (TokenSource, error)  // default
  func OAuth2TokenSource(c Credentials, scopes ...string) (TokenSource, error)
  func MetadataTokenSource() TokenSource
  func StaticTokenSource(t Token) TokenSource
  func Cached(ts TokenSource) TokenSource
linkage: >
  JWTTokenSource and OAuth2TokenSource are the only entry points that reference
  the signing code, so a binary built with StaticTokenSource or
  MetadataTokenSource drops it. Revised 2026-08-02: with
  decision:native-rsa-signing that saves ~131 KB, not ~590 KB, so it is a
  deployment choice rather than a size choice.
signer: >
  the signature itself comes from api:rsa-signer, which is crypto/rsa on host go
  and the OS or mbedTLS under tinygo. cloud/google owns the credential file, the
  claims and the cache; it does not own the modular exponentiation.
signing: |
  func SignJWT(c Credentials, claims jwt.Claims) (string, error)
  // Claims, Header and the compact-serialization discipline come from the
  // moved-in jwt package; see decision:jwt-package-reuse. The RSA key handling
  // and PKCS#8 parsing stay here, because neither is a JWT concern.
  // RS256 only. ES256 is not implemented; see requirement:google-auth-validation.
key_size: >
  2048 bit. A 4096-bit key costs 19.5ms per signature on the pure-Go tinygo
  path, against 2.9ms; api:rsa-signer's native backends remove most of that
  gap. Measured in requirement:google-auth-validation. The constructor does not
  refuse a larger key; it is documented, not enforced.
caching:
  policy: Cached refreshes 60s before Expiry, on the calling goroutine
  concurrency: one in-flight refresh, the losers wait for it
  no_goroutine: deliberate; see decision:google-token-strategy
environment: |
  func ProjectIDFromEnv() string    // GOOGLE_CLOUD_PROJECT, then DATASTORE_PROJECT_ID
  func EmulatorHost(service string) string  // DATASTORE_EMULATOR_HOST for datastore
transport: |
  type ClientOptions struct { Timeout time.Duration; MaxIdleConnsPerHost int }
  func NewHTTPClient(opts ClientOptions) *http.Client
  func CloseIdleConnections(c *http.Client)
  const Backend string     // "net/http" or "https", same values as cloud/aws
errors:
  sentinels: ErrNoCredentials, ErrNoProject, ErrTokenExpired, ErrBadPrivateKey
  clock_skew: >
    a token the server rejects as expired is a UNAUTHENTICATED reply, not a
    local error. The client reports it with a hint naming the system clock,
    because that is the cause a device in the field will actually have.
non_goals:
  - no credential chain discovery beyond the environment variable and an explicit file
  - no impersonation, workload identity federation, or external account files
  - no per-service endpoint resolution; a service package builds its own URL
  - no retry policy, which is per-service; see requirement:datastore-retry-policy
build_tags: rule:build-tag-selection
decided_by: decision:google-shared-package, decision:google-token-strategy
evidence: requirement:google-auth-validation
consumers: api:datastore-client
counterpart: api:aws-signer
