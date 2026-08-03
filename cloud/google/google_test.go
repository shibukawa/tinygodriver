package google

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shibukawa/tinygodriver/jwt"
)

// These tests carry no build tag, so the signing path runs against crypto/rsa
// on a plain build and against the native backend under -tags
// force_tinygo_logic. A token that only mints correctly on one of them fails
// here.

func testCredentials(t *testing.T) Credentials {
	t.Helper()
	key, err := os.ReadFile("../../internal/rsasign/testdata/rsa2048.pkcs8.pem")
	if err != nil {
		t.Fatalf("read test key: %v", err)
	}
	return Credentials{
		Type:         "service_account",
		ProjectID:    "test-project",
		PrivateKeyID: "key-1",
		PrivateKey:   string(key),
		ClientEmail:  "svc@test-project.iam.gserviceaccount.com",
		TokenURI:     defaultTokenURI,
	}
}

func fixedClock(at time.Time) func() time.Time { return func() time.Time { return at } }

func TestCredentialsFromJSON(t *testing.T) {
	c := testCredentials(t)
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	got, err := CredentialsFromJSON(raw)
	if err != nil {
		t.Fatalf("CredentialsFromJSON: %v", err)
	}
	if got.ClientEmail != c.ClientEmail || got.ProjectID != c.ProjectID {
		t.Errorf("round trip lost fields: %+v", got)
	}

	for _, bad := range []string{`not json`, `{}`, `{"client_email":"a@b"}`} {
		if _, err := CredentialsFromJSON([]byte(bad)); err != ErrNoCredentials {
			t.Errorf("CredentialsFromJSON(%q) err = %v, want ErrNoCredentials", bad, err)
		}
	}
}

func TestCredentialsFromJSONDefaultsTokenURI(t *testing.T) {
	c := testCredentials(t)
	c.TokenURI = ""
	raw, _ := json.Marshal(c)
	got, err := CredentialsFromJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.TokenURI != defaultTokenURI {
		t.Errorf("TokenURI = %q, want the default", got.TokenURI)
	}
}

func TestEmulatorHost(t *testing.T) {
	t.Setenv("DATASTORE_EMULATOR_HOST", "127.0.0.1:8081")
	if got := EmulatorHost("datastore"); got != "127.0.0.1:8081" {
		t.Errorf("EmulatorHost = %q", got)
	}
	if got := EmulatorHost("firestore"); got != "" {
		t.Errorf("unset service returned %q", got)
	}
	if got := EmulatorHost(""); got != "" {
		t.Errorf("empty service returned %q", got)
	}
}

// TestSelfSignedJWTShape checks the claims Google actually requires. Getting
// any of these wrong produces a token the service rejects with
// UNAUTHENTICATED, which is not retryable, so they are worth pinning offline.
func TestSelfSignedJWTShape(t *testing.T) {
	c := testCredentials(t)
	src, err := JWTTokenSource(c, "https://datastore.googleapis.com/")
	if err != nil {
		t.Fatalf("JWTTokenSource: %v", err)
	}
	defer src.Close()

	now := time.Unix(1_800_000_000, 0)
	src.Now = fixedClock(now)

	token, err := src.Token(context.Background())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if !token.Expiry.Equal(now.Add(tokenLifetime)) {
		t.Errorf("Expiry = %v, want %v", token.Expiry, now.Add(tokenLifetime))
	}

	segments := strings.Split(token.Value, ".")
	if len(segments) != 3 {
		t.Fatalf("compact segments = %d, want 3", len(segments))
	}

	var header struct {
		Algorithm string `json:"alg"`
		Type      string `json:"typ"`
		KeyID     string `json:"kid"`
	}
	decodeSegment(t, segments[0], &header)
	if header.Algorithm != "RS256" {
		t.Errorf("alg = %q, want RS256", header.Algorithm)
	}
	if header.Type != "JWT" {
		t.Errorf("typ = %q, want JWT", header.Type)
	}
	if header.KeyID != c.PrivateKeyID {
		t.Errorf("kid = %q, want %q", header.KeyID, c.PrivateKeyID)
	}

	var claims map[string]any
	decodeSegment(t, segments[1], &claims)
	if claims["iss"] != c.ClientEmail {
		t.Errorf("iss = %v", claims["iss"])
	}
	if claims["sub"] != c.ClientEmail {
		t.Errorf("sub = %v", claims["sub"])
	}
	// aud must be a bare string, not an array: that is the form Google's
	// self-signed JWT documentation shows.
	aud, ok := claims["aud"].(string)
	if !ok || aud != "https://datastore.googleapis.com/" {
		t.Errorf("aud = %#v, want a bare string", claims["aud"])
	}
	// scope and aud are mutually exclusive on this path.
	if _, present := claims["scope"]; present {
		t.Error("a self-signed JWT must not carry scope alongside aud")
	}
	if claims["iat"] != float64(now.Unix()) {
		t.Errorf("iat = %v, want %v", claims["iat"], now.Unix())
	}
	if claims["exp"] != float64(now.Add(tokenLifetime).Unix()) {
		t.Errorf("exp = %v", claims["exp"])
	}
}

// TestSelfSignedJWTVerifies closes the loop through the verifier, so a
// signature that is well-formed but wrong cannot pass the shape test alone.
func TestSelfSignedJWTVerifies(t *testing.T) {
	c := testCredentials(t)
	src, err := JWTTokenSource(c, "https://datastore.googleapis.com/")
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	now := time.Unix(1_800_000_000, 0)
	src.Now = fixedClock(now)

	token, err := src.Token(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	public := publicKeyFor(t, c)
	claims, err := jwt.ParseAndVerify(token.Value,
		jwt.KeyResolverFunc(func(h jwt.Header) (jwt.VerificationKey, error) {
			if h.KeyID != c.PrivateKeyID {
				return jwt.VerificationKey{}, jwt.ErrKeyNotFound
			}
			return jwt.VerificationKey{Algorithm: "RS256", RSA: public}, nil
		}),
		jwt.ParseOptions{},
		jwt.VerifyOptions{
			AllowedAlgorithms: []string{"RS256"},
			Issuer:            c.ClientEmail,
			Audience:          "https://datastore.googleapis.com/",
			TokenType:         "JWT",
			Clock:             fixedClock(now.Add(time.Minute)),
		})
	if err != nil {
		t.Fatalf("backend %q minted a token the verifier rejects: %v", SignerBackend(), err)
	}
	if claims.Subject != c.ClientEmail {
		t.Errorf("sub = %q", claims.Subject)
	}
}

func TestJWTTokenSourceRequiresAudience(t *testing.T) {
	if _, err := JWTTokenSource(testCredentials(t), ""); err == nil {
		t.Error("empty audience was accepted")
	}
}

func TestNewRSASignerRejectsBadKey(t *testing.T) {
	c := testCredentials(t)
	c.PrivateKey = "-----BEGIN PRIVATE KEY-----\nnope\n-----END PRIVATE KEY-----\n"
	if _, err := NewRSASigner(c); err != ErrBadPrivateKey {
		t.Errorf("err = %v, want ErrBadPrivateKey", err)
	}
	if _, err := NewRSASigner(Credentials{}); err != ErrNoCredentials {
		t.Errorf("empty credentials err = %v, want ErrNoCredentials", err)
	}
}

func TestStaticTokenSource(t *testing.T) {
	want := Token{Value: "abc", Expiry: time.Unix(2_000_000_000, 0)}
	got, err := StaticTokenSource(want).Token(context.Background())
	if err != nil || got != want {
		t.Errorf("Token = %+v, %v", got, err)
	}
	if _, err := StaticTokenSource(Token{}).Token(context.Background()); err != ErrNoCredentials {
		t.Errorf("empty token err = %v", err)
	}
}

// countingSource records how often the wrapped source was actually consulted.
type countingSource struct {
	mu     sync.Mutex
	calls  int
	expiry time.Time
}

func (c *countingSource) Token(context.Context) (Token, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	return Token{Value: fmt.Sprintf("token-%d", c.calls), Expiry: c.expiry}, nil
}

func TestCachedReusesUntilRefreshMargin(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	inner := &countingSource{expiry: now.Add(time.Hour)}
	cached := Cached(inner)
	clock := now
	cached.Now = func() time.Time { return clock }

	first, err := cached.Token(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		got, err := cached.Token(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if got.Value != first.Value {
			t.Fatalf("token changed while still valid: %q then %q", first.Value, got.Value)
		}
	}
	if inner.calls != 1 {
		t.Errorf("inner source called %d times, want 1", inner.calls)
	}

	// Inside the refresh margin the token is replaced even though it has not
	// technically expired.
	clock = now.Add(time.Hour).Add(-refreshMargin / 2)
	inner.expiry = clock.Add(time.Hour)
	second, err := cached.Token(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if second.Value == first.Value {
		t.Error("token was not refreshed inside the margin")
	}
	if inner.calls != 2 {
		t.Errorf("inner source called %d times, want 2", inner.calls)
	}
}

func TestCachedInvalidateForcesRefresh(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	inner := &countingSource{expiry: now.Add(time.Hour)}
	cached := Cached(inner)
	cached.Now = fixedClock(now)

	first, _ := cached.Token(context.Background())
	cached.Invalidate()
	second, err := cached.Token(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first.Value == second.Value {
		t.Error("Invalidate did not force a new token")
	}
}

func TestCachedIsConcurrencySafe(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	inner := &countingSource{expiry: now.Add(time.Hour)}
	cached := Cached(inner)
	cached.Now = fixedClock(now)

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := cached.Token(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	// One refresh in flight, the losers wait for it.
	if inner.calls != 1 {
		t.Errorf("inner source called %d times under concurrency, want 1", inner.calls)
	}
}

func TestOAuth2Exchange(t *testing.T) {
	c := testCredentials(t)
	var gotForm struct {
		grantType string
		assertion string
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse form: %v", err)
		}
		gotForm.grantType = r.PostForm.Get("grant_type")
		gotForm.assertion = r.PostForm.Get("assertion")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"access_token":"ya29.test","token_type":"Bearer","expires_in":3599}`)
	}))
	defer server.Close()

	c.TokenURI = server.URL
	src, err := OAuth2TokenSource(c, "https://www.googleapis.com/auth/datastore")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	src.Now = fixedClock(now)
	src.HTTPClient = server.Client()

	token, err := src.Token(context.Background())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if token.Value != "ya29.test" {
		t.Errorf("Value = %q", token.Value)
	}
	if want := now.Add(3599 * time.Second); !token.Expiry.Equal(want) {
		t.Errorf("Expiry = %v, want %v", token.Expiry, want)
	}
	if gotForm.grantType != "urn:ietf:params:oauth:grant-type:jwt-bearer" {
		t.Errorf("grant_type = %q", gotForm.grantType)
	}

	// The assertion is audienced at the token endpoint and carries the scope,
	// which is exactly what a self-signed JWT must not do.
	var claims map[string]any
	decodeSegment(t, strings.Split(gotForm.assertion, ".")[1], &claims)
	if claims["aud"] != server.URL {
		t.Errorf("assertion aud = %v, want the token endpoint", claims["aud"])
	}
	if claims["scope"] != "https://www.googleapis.com/auth/datastore" {
		t.Errorf("assertion scope = %v", claims["scope"])
	}
}

func TestOAuth2RequiresScope(t *testing.T) {
	if _, err := OAuth2TokenSource(testCredentials(t)); err == nil {
		t.Error("no scopes was accepted")
	}
}

func TestMetadataTokenSource(t *testing.T) {
	var gotFlavor string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotFlavor = r.Header.Get("Metadata-Flavor")
		fmt.Fprint(w, `{"access_token":"metadata-token","token_type":"Bearer","expires_in":1800}`)
	}))
	defer server.Close()

	now := time.Unix(1_800_000_000, 0)
	src := MetadataTokenSource()
	src.URL = server.URL
	src.HTTPClient = server.Client()
	src.Now = fixedClock(now)

	token, err := src.Token(context.Background())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if token.Value != "metadata-token" {
		t.Errorf("Value = %q", token.Value)
	}
	if gotFlavor != "Google" {
		t.Errorf("Metadata-Flavor = %q; the server refuses a request without it", gotFlavor)
	}
}

func TestTokenResponseRejection(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	for _, body := range []string{
		`not json`,
		`{}`,
		`{"access_token":"x"}`,
		`{"access_token":"x","expires_in":0}`,
		`{"expires_in":3600}`,
	} {
		if _, err := decodeTokenResponse([]byte(body), now); err == nil {
			t.Errorf("accepted %s", body)
		}
	}
}

func TestTokenEndpointErrorIsReported(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":"invalid_grant"}`)
	}))
	defer server.Close()

	c := testCredentials(t)
	c.TokenURI = server.URL
	src, err := OAuth2TokenSource(c, "scope")
	if err != nil {
		t.Fatal(err)
	}
	src.HTTPClient = server.Client()
	_, err = src.Token(context.Background())
	if err == nil || !strings.Contains(err.Error(), "invalid_grant") {
		t.Errorf("err = %v, want the server message carried through", err)
	}
}

func TestTokenValid(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	cases := []struct {
		name  string
		token Token
		want  bool
	}{
		{"empty", Token{}, false},
		{"expired", Token{Value: "x", Expiry: now.Add(-time.Second)}, false},
		{"inside margin", Token{Value: "x", Expiry: now.Add(refreshMargin / 2)}, false},
		{"fresh", Token{Value: "x", Expiry: now.Add(time.Hour)}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.token.Valid(now); got != c.want {
				t.Errorf("Valid = %v, want %v", got, c.want)
			}
		})
	}
}

// publicKeyFor derives the verification key from the same credentials the
// signer used. crypto/x509 is free here: this is a test, on host Go, and the
// property it protects is that the shipped binary contains none of it.
func publicKeyFor(t *testing.T, c Credentials) *rsa.PublicKey {
	t.Helper()
	blk, _ := pem.Decode([]byte(c.PrivateKey))
	if blk == nil {
		t.Fatal("credentials carried no PEM block")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(blk.Bytes)
	if err != nil {
		t.Fatalf("parse test key: %v", err)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		t.Fatal("test key is not RSA")
	}
	return &key.PublicKey
}

func decodeSegment(t *testing.T, segment string, into any) {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(segment)
	if err != nil {
		t.Fatalf("decode segment: %v", err)
	}
	if err := json.Unmarshal(raw, into); err != nil {
		t.Fatalf("unmarshal segment %s: %v", raw, err)
	}
}
