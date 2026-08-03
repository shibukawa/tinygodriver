// Package google holds what every Google Cloud client in this repository
// needs: credentials, bearer tokens, and the HTTP client the build selects.
//
// It is the counterpart of cloud/aws, and it is shaped by the one real
// difference between the two clouds. AWS signs each request locally and the
// signature never leaves the process. Google wants a bearer token, and the
// ordinary way to get one is a POST to a second host — which on this stack
// means a second TLS handshake to a second pool entry before the first real
// call can go out.
//
// So the default here is a self-signed JWT: sign a claim set with the service
// account key, send it as the bearer value, and skip the token endpoint
// entirely. Google documents this for Cloud APIs, with the service host as the
// audience.
//
//	creds, _ := google.CredentialsFromEnv()
//	ts, _ := google.JWTTokenSource(creds, "https://datastore.googleapis.com/")
//	token, _ := google.Cached(ts).Token(ctx)
//	req.Header.Set("Authorization", "Bearer "+token.Value)
//
// The RSA operation itself lives in internal/rsasign, which uses crypto/rsa on
// host Go and the OS crypto library on TinyGo builds. That split is about
// binary size: crypto/rsa plus crypto/x509 cost about 588 KB, on targets where
// the whole HTTPS client is 272 KB.
//
// Only JWTTokenSource and OAuth2TokenSource reference the signing code, so a
// binary built with StaticTokenSource or MetadataTokenSource drops it.
//
// Credentials come from a service account file or from an explicitly supplied
// token. There is no credential chain discovery beyond the environment
// variable, no impersonation, and no workload identity federation.
//
// One failure mode has no AWS equivalent and is worth stating: a self-signed
// JWT is only valid against the server's clock. A device with a wrong clock
// mints a token the server rejects as expired, reported as UNAUTHENTICATED,
// which is not a retryable error. The likeliest cause of that status in the
// field is the clock, not the key.
package google
