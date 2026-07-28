// Package https provides a net/http-compatible HTTPS client for TinyGo.
//
// TinyGo ships a stub crypto/tls, so net/http cannot reach https URLs. This
// package fills that gap by performing TLS through the TLS stack the host OS
// already provides, and by exposing the familiar net/http surface:
//
//	resp, err := https.Get("https://example.com/")
//
// Request and response types are the standard net/http types, so replacing
// http.Get with https.Get is usually the only change an application needs.
//
// # Implementation selection
//
//   - standard Go builds delegate to net/http and crypto/tls
//   - TinyGo on macOS uses Network.framework
//   - TinyGo on Linux uses vendored mbedTLS
//   - TinyGo on Windows uses Schannel
//   - other TinyGo targets return ErrPlatformNotSupported
//   - go build -tags force_tinygo_logic forces the native backend on host Go,
//     which is how the native code is tested without a TinyGo toolchain
//
// A build never falls back to plaintext or to an unverified connection.
//
// # Configuration
//
// Config is deliberately not crypto/tls.Config, because TinyGo builds must not
// link crypto/tls. It accepts PEM bytes, which every backend understands:
//
//	client := https.NewClient(
//		https.WithRootCAFile("/etc/ssl/private-ca.pem"),
//	)
//
// The zero value verifies the peer chain and hostname against the system trust
// store and requires TLS 1.2 or later. WithInsecureSkipVerify disables that and
// is for testing only.
//
// # Errors
//
// Native status codes are mapped onto sentinel errors, so application code can
// branch identically on every platform:
//
//	if errors.Is(err, https.ErrUntrustedRoot) { ... }
package https
