//go:build (tinygo || force_tinygo_logic) && ((linux && !wasip2) || (darwin && darwinstarttlswith13))

package https

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// mbedTLS has no system trust store, so the anchors are loaded here and passed
// in as PEM. The first two locations follow OpenSSL convention so existing
// deployment tooling keeps working; the rest cover the common distro layouts.
var systemCAFiles = []string{
	"/etc/ssl/certs/ca-certificates.crt", // Debian, Ubuntu, Alpine
	"/etc/pki/tls/certs/ca-bundle.crt",   // RHEL, Fedora
	"/etc/ssl/ca-bundle.pem",             // SUSE
	"/etc/ssl/cert.pem",                  // Alpine, some others
}

// systemCADirs are scanned only when no bundle file is found.
var systemCADirs = []string{
	"/etc/ssl/certs",
	"/etc/pki/tls/certs",
}

var (
	systemCAOnce sync.Once
	systemCAPEM  []byte
	systemCAErr  error
)

// systemCertPool returns the concatenated PEM of the system trust store.
// The bundle is large, so it is read once per process.
func systemCertPool() ([]byte, error) {
	systemCAOnce.Do(func() {
		systemCAPEM, systemCAErr = loadSystemCertPool()
	})
	return systemCAPEM, systemCAErr
}

func loadSystemCertPool() ([]byte, error) {
	if f := os.Getenv("SSL_CERT_FILE"); f != "" {
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, fmt.Errorf("https: SSL_CERT_FILE %q: %w", f, err)
		}
		return data, nil
	}

	if d := os.Getenv("SSL_CERT_DIR"); d != "" {
		if pem, err := readCADir(d); err == nil && len(pem) > 0 {
			return pem, nil
		}
	}

	for _, f := range systemCAFiles {
		if data, err := os.ReadFile(f); err == nil && len(data) > 0 {
			return data, nil
		}
	}

	for _, d := range systemCADirs {
		if pem, err := readCADir(d); err == nil && len(pem) > 0 {
			return pem, nil
		}
	}

	// Never fall through to an empty pool: that would make every connection
	// fail verification in a way that looks like a server problem.
	return nil, fmt.Errorf(
		"https: no system CA bundle found; looked at $SSL_CERT_FILE, $SSL_CERT_DIR, %v and %v",
		systemCAFiles, systemCADirs)
}

func readCADir(dir string) ([]byte, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []byte
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		switch filepath.Ext(e.Name()) {
		case ".pem", ".crt":
		default:
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		out = append(out, data...)
		if len(out) > 0 && out[len(out)-1] != '\n' {
			out = append(out, '\n')
		}
	}
	return out, nil
}

// anchorsFor builds the PEM blob handed to the backend for one dial.
func anchorsFor(cfg *Config) ([]byte, error) {
	// mbedTLS tolerates unparseable entries in a bundle, which is right for a
	// system store but would silently swallow a caller's malformed PEM. Reject
	// that here so every backend reports it the same way.
	if _, err := cfg.rootCADER(); err != nil {
		return nil, err
	}

	var out []byte
	if cfg == nil || !cfg.RootCAsOnly {
		sys, err := systemCertPool()
		if err != nil {
			// Only fatal when the caller supplied no anchors of their own.
			if cfg == nil || len(cfg.RootCAs) == 0 {
				return nil, err
			}
		} else {
			out = append(out, sys...)
		}
	}
	if cfg != nil {
		for _, pem := range cfg.RootCAs {
			out = append(out, pem...)
			if len(out) > 0 && out[len(out)-1] != '\n' {
				out = append(out, '\n')
			}
		}
	}
	return out, nil
}
