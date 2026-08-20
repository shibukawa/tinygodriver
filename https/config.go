package https

import (
	"encoding/pem"
	"errors"
	"os"
)

// Version identifies a TLS protocol version. The values match the wire
// encoding used by crypto/tls, but the type is defined here so TinyGo builds
// never need to import crypto/tls.
type Version uint16

const (
	VersionTLS12 Version = 0x0303
	VersionTLS13 Version = 0x0304
)

// KeyPair is a PEM-encoded client certificate and its private key.
type KeyPair struct {
	CertPEM []byte
	KeyPEM  []byte
}

// Config holds client TLS settings. The zero value verifies the peer chain and
// hostname against the system trust store and requires TLS 1.2 or later.
//
// Config is deliberately not crypto/tls.Config: the native backends accept PEM
// bytes, and TinyGo builds must not link crypto/tls.
type Config struct {
	// RootCAs are additional PEM-encoded trust anchors.
	RootCAs [][]byte

	// RootCAsOnly ignores the system trust store, trusting only RootCAs.
	RootCAsOnly bool

	// Certificates are client certificates offered for mutual TLS.
	Certificates []KeyPair

	// InsecureSkipVerify disables chain and hostname verification.
	// It is for testing only.
	InsecureSkipVerify bool

	// ServerName overrides the SNI name and the name checked against the
	// certificate. It defaults to the host in the request URL.
	ServerName string

	// MinVersion is the minimum acceptable TLS version. Zero means TLS 1.2.
	MinVersion Version

	// err records a failure from an Option, reported when the Config is used.
	err error
}

// Option configures a Config.
type Option func(*Config)

// WithRootCAPEM adds PEM-encoded trust anchors.
func WithRootCAPEM(pemBytes []byte) Option {
	return func(c *Config) {
		c.RootCAs = append(c.RootCAs, pemBytes)
	}
}

// WithRootCAFile adds trust anchors read from a PEM file. A read failure is
// reported when the Config is first used.
func WithRootCAFile(path string) Option {
	return func(c *Config) {
		data, err := os.ReadFile(path)
		if err != nil {
			if c.err == nil {
				c.err = err
			}
			return
		}
		c.RootCAs = append(c.RootCAs, data)
	}
}

// WithRootCAsOnly ignores the system trust store.
func WithRootCAsOnly(only bool) Option {
	return func(c *Config) { c.RootCAsOnly = only }
}

// WithClientCertificate offers a PEM-encoded client certificate for mutual TLS.
func WithClientCertificate(certPEM, keyPEM []byte) Option {
	return func(c *Config) {
		c.Certificates = append(c.Certificates, KeyPair{CertPEM: certPEM, KeyPEM: keyPEM})
	}
}

// WithInsecureSkipVerify disables certificate verification. Testing only.
func WithInsecureSkipVerify(skip bool) Option {
	return func(c *Config) { c.InsecureSkipVerify = skip }
}

// WithServerName overrides the SNI and verified host name.
func WithServerName(name string) Option {
	return func(c *Config) { c.ServerName = name }
}

// WithMinVersion sets the minimum TLS version.
func WithMinVersion(v Version) Option {
	return func(c *Config) { c.MinVersion = v }
}

// NewConfig builds a Config from options.
func NewConfig(opts ...Option) *Config {
	c := &Config{}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func (c *Config) minVersion() Version {
	if c == nil || c.MinVersion == 0 {
		return VersionTLS12
	}
	return c.MinVersion
}

// rootCADER decodes every configured PEM anchor into DER certificates.
// PEM decoding happens in Go so the native backends never need a base64
// decoder, and so TinyGo builds avoid crypto/x509.
func (c *Config) rootCADER() ([][]byte, error) {
	if c == nil {
		return nil, nil
	}
	var ders [][]byte
	for _, blob := range c.RootCAs {
		rest := blob
		found := false
		for {
			var block *pem.Block
			block, rest = pem.Decode(rest)
			if block == nil {
				break
			}
			if block.Type != "CERTIFICATE" {
				continue
			}
			ders = append(ders, block.Bytes)
			found = true
		}
		if !found {
			return nil, errors.New("https: no CERTIFICATE block in PEM data")
		}
	}
	return ders, nil
}
