//go:build !tinygo && !force_tinygo_logic

package mysql

import (
	"crypto/tls"
	"crypto/x509"
	"errors"

	upstream "github.com/go-sql-driver/mysql"
	"github.com/shibukawa/tinygodriver/https"
)

// Backend identifies the implementation selected by build constraints.
const Backend = "go-sql-driver"

// RegisterTLSConfig registers trust settings under a name usable as tls=<name>
// in a DSN. This backend speaks crypto/tls, so the PEM bytes in cfg are parsed
// into a tls.Config here.
func RegisterTLSConfig(name string, cfg *https.Config) error {
	std, err := stdTLSConfig(cfg)
	if err != nil {
		return err
	}
	return upstream.RegisterTLSConfig(name, std)
}

// DeregisterTLSConfig removes a configuration registered by RegisterTLSConfig.
func DeregisterTLSConfig(name string) { upstream.DeregisterTLSConfig(name) }

func stdTLSConfig(c *https.Config) (*tls.Config, error) {
	if c == nil {
		return &tls.Config{MinVersion: tls.VersionTLS12}, nil
	}
	minVersion := uint16(c.MinVersion)
	if minVersion == 0 {
		minVersion = tls.VersionTLS12
	}
	out := &tls.Config{
		InsecureSkipVerify: c.InsecureSkipVerify,
		ServerName:         c.ServerName,
		MinVersion:         minVersion,
	}
	if len(c.RootCAs) > 0 || c.RootCAsOnly {
		var pool *x509.CertPool
		if c.RootCAsOnly {
			pool = x509.NewCertPool()
		} else {
			var err error
			if pool, err = x509.SystemCertPool(); err != nil {
				pool = x509.NewCertPool()
			}
		}
		for _, blob := range c.RootCAs {
			if !pool.AppendCertsFromPEM(blob) {
				return nil, errors.New("mysql: no CERTIFICATE block in PEM data")
			}
		}
		out.RootCAs = pool
	}
	for _, kp := range c.Certificates {
		cert, err := tls.X509KeyPair(kp.CertPEM, kp.KeyPEM)
		if err != nil {
			return nil, err
		}
		out.Certificates = append(out.Certificates, cert)
	}
	return out, nil
}
