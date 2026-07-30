//go:build !tinygo && !force_tinygo_logic

package https

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/http"
	"sync"
)

const backendName = "crypto/tls"

// stdTransport lazily builds the net/http.Transport that does the real work.
type stdTransport struct {
	once sync.Once
	rt   http.RoundTripper
	err  error
}

// connPool is unused on this path, where net/http owns connection reuse. It
// keeps the Transport struct identical across build configurations.
type connPool struct{}

// CloseIdleConnections closes connections that are being kept for reuse. It
// mirrors net/http.Transport.CloseIdleConnections and does not affect requests
// still in flight.
func (t *Transport) CloseIdleConnections() {
	if rt, ok := t.std.rt.(*http.Transport); ok {
		rt.CloseIdleConnections()
	}
}

// roundTrip delegates to net/http so behavior — redirects, keep-alive, gzip,
// chunked bodies, HTTP/2 — matches the standard library exactly.
func (t *Transport) roundTrip(req *http.Request) (*http.Response, error) {
	t.std.once.Do(func() {
		tlsCfg, err := t.tlsConfig()
		if err != nil {
			t.std.err = err
			return
		}
		base := http.DefaultTransport.(*http.Transport).Clone()
		base.TLSClientConfig = tlsCfg
		base.TLSHandshakeTimeout = t.dialTimeout()
		base.ResponseHeaderTimeout = t.ResponseTimeout
		// The pooling knobs mean the same thing on both paths, so a program
		// that sets them does not change behavior with the build tag. Zero
		// keeps net/http's own defaults, which differ from the native path's
		// only because net/http can check a connection before reusing it.
		if t.MaxIdleConnsPerHost > 0 {
			base.MaxIdleConnsPerHost = t.MaxIdleConnsPerHost
		}
		if t.IdleConnTimeout > 0 {
			base.IdleConnTimeout = t.IdleConnTimeout
		}
		base.DisableKeepAlives = t.DisableKeepAlives
		t.std.rt = base
	})
	if t.std.err != nil {
		if req.Body != nil {
			req.Body.Close()
		}
		return nil, t.std.err
	}
	resp, err := t.std.rt.RoundTrip(req)
	if err != nil {
		host := ""
		if req.URL != nil {
			host = req.URL.Host
		}
		return nil, classifyStdError("dial", host, err)
	}
	return resp, nil
}

// tlsConfig converts the backend-neutral Config into a crypto/tls.Config.
func (t *Transport) tlsConfig() (*tls.Config, error) {
	c := t.Config
	if c == nil {
		return &tls.Config{MinVersion: uint16(VersionTLS12)}, nil
	}

	out := &tls.Config{
		InsecureSkipVerify: c.InsecureSkipVerify,
		ServerName:         c.ServerName,
		MinVersion:         uint16(c.minVersion()),
	}

	if len(c.RootCAs) > 0 {
		var pool *x509.CertPool
		if c.RootCAsOnly {
			pool = x509.NewCertPool()
		} else {
			var err error
			pool, err = x509.SystemCertPool()
			if err != nil {
				pool = x509.NewCertPool()
			}
		}
		for _, blob := range c.RootCAs {
			if !pool.AppendCertsFromPEM(blob) {
				return nil, errors.New("https: no CERTIFICATE block in PEM data")
			}
		}
		out.RootCAs = pool
	} else if c.RootCAsOnly {
		out.RootCAs = x509.NewCertPool()
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

// classifyStdError maps crypto/tls and crypto/x509 errors onto the same
// sentinels the native backends produce. op is "dial" or "upgrade".
func classifyStdError(op, host string, err error) error {
	wrap := func(sentinel error) error {
		return &Error{Op: op, Host: host, Backend: backendName, Err: sentinel}
	}

	var hostErr x509.HostnameError
	if errors.As(err, &hostErr) {
		return wrap(ErrHostnameMismatch)
	}
	var authErr x509.UnknownAuthorityError
	if errors.As(err, &authErr) {
		return wrap(ErrUntrustedRoot)
	}
	var invalidErr x509.CertificateInvalidError
	if errors.As(err, &invalidErr) {
		if invalidErr.Reason == x509.Expired {
			return wrap(ErrCertificateExpired)
		}
		return wrap(ErrCertificateInvalid)
	}
	var recordErr tls.RecordHeaderError
	if errors.As(err, &recordErr) {
		return wrap(ErrHandshakeFailed)
	}
	var alert tls.AlertError
	if errors.As(err, &alert) {
		return wrap(ErrHandshakeFailed)
	}
	// Not a TLS failure; return it untouched so net/http semantics survive.
	return err
}
