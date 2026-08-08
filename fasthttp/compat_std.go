//go:build !tinygo

// The standard-Go half of the fork's divergences. Everything here is upstream
// fasthttp's own behaviour, moved out of the file it came from so the TinyGo
// half can differ. See PATCHES.md.
//
// The constraint is on tinygo alone, without force_tinygo_logic. Every
// divergence in this package exists because a crypto/tls or net symbol is
// missing from TinyGo, not because the logic differs, so there is nothing for a
// host-Go build to exercise -- and cloneTLSConfig would copy the mutex inside a
// real tls.Config, which vet rightly rejects.

package fasthttp

import (
	"crypto/tls"
	"io"
	"net"
	"os"
)

// Backend identifies the implementation selected by build constraints.
const Backend = "crypto/tls"

// resolveInDialer says whether name resolution is the dialer's job. On standard
// Go it is not: TCPDialer resolves and caches addresses itself.
const resolveInDialer = false

// tlsConnImpl is the concrete TLS connection type that perIPTLSConn embeds.
type tlsConnImpl = tls.Conn

func cloneTLSConfig(c *tls.Config) *tls.Config { return c.Clone() }

func negotiatedProtocol(tc tlsConn) string {
	return tc.ConnectionState().NegotiatedProtocol
}

func tlsClient(conn net.Conn, cfg *tls.Config) (tlsClientConn, error) {
	return tls.Client(conn, cfg), nil
}

func newTLSListener(ln net.Listener, cfg *tls.Config) (net.Listener, error) {
	return tls.NewListener(ln, cfg), nil
}

func x509KeyPair(certData, keyData []byte) (tls.Certificate, error) {
	if len(certData) == 0 && len(keyData) == 0 {
		return tls.Certificate{}, errNoCertOrKeyProvided
	}
	cert, err := tls.X509KeyPair(certData, keyData)
	if err != nil {
		return tls.Certificate{}, errCannotLoadTLSKeyPair(len(certData), len(keyData), err)
	}
	return cert, nil
}

func defaultResolver() Resolver { return net.DefaultResolver }

// copyZeroAlloc is upstream's, unchanged. It lives here because its sendfile
// fast paths need methods TinyGo does not define.
func copyZeroAlloc(w io.Writer, r io.Reader) (int64, error) {
	var readerIsFile, readerIsConn bool

	switch r := r.(type) {
	case *os.File:
		readerIsFile = true
	case *net.TCPConn:
		readerIsConn = true
	case io.WriterTo:
		return r.WriteTo(w)
	}

	switch w := w.(type) {
	case *os.File:
		if readerIsConn {
			return w.ReadFrom(r)
		}
	case *net.TCPConn:
		if readerIsFile {
			// net.WriteTo requires go1.22 or later
			// Benchmark tests show that on Windows, WriteTo performs
			// significantly better than ReadFrom. On Linux, however,
			// ReadFrom slightly outperforms WriteTo. When possible,
			// copyZeroAlloc aims to perform  better than or as well
			// as io.Copy, so we use WriteTo whenever possible for
			// optimal performance.
			if rt, ok := r.(io.WriterTo); ok {
				return rt.WriteTo(w)
			}
			return w.ReadFrom(r)
		}
	case io.ReaderFrom:
		return w.ReadFrom(r)
	}

	vbuf := copyBufPool.Get()
	buf := vbuf.([]byte) //nolint:forcetypeassert
	n, err := copyBuffer(w, r, buf)
	copyBufPool.Put(vbuf)
	return n, err
}
