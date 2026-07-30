//go:build tinygo || force_tinygo_logic

package https

import (
	"errors"
	"net"
	"net/url"
	"os"
	"strings"
)

// Proxy selection for the native path.
//
// Standard Go builds get this from net/http, whose DefaultTransport carries
// ProxyFromEnvironment. The native path builds its own connections, so it has
// to read the same environment itself or a machine behind a mandatory proxy
// cannot reach anything. That is not a hypothetical: it presents as a dial
// failure naming a socket error, with nothing pointing at a proxy.
//
// The variables and their precedence match net/http, including the lowercase
// spellings, so a program does not behave differently depending on which build
// tag produced it.

// ErrProxyScheme reports a proxy URL this build cannot use.
//
// An https:// proxy would mean running TLS to the proxy and then the origin's
// TLS inside that tunnel. Every native backend here starts from a descriptor,
// so there is no socket for the inner session to use. Refusing is deliberate:
// quietly connecting direct would send traffic the deployment expects to be
// proxied, and quietly dropping to a plaintext hop would be worse.
var ErrProxyScheme = errors.New("https: unsupported proxy scheme")

// proxy describes the hop to make instead of connecting to the origin.
type proxy struct {
	Host string
	Port string
	Auth string // pre-encoded Proxy-Authorization value, empty when none
}

// proxyFor reports the proxy to use for one request, or nil for a direct
// connection. secure selects HTTPS_PROXY over HTTP_PROXY.
func proxyFor(host, port string, secure bool) (*proxy, error) {
	raw := proxyEnv(secure)
	if raw == "" {
		return nil, nil
	}
	if useProxy := !matchNoProxy(host, port); !useProxy {
		return nil, nil
	}

	u, err := parseProxyURL(raw)
	if err != nil {
		return nil, err
	}
	return u, nil
}

// proxyEnv reads the variable pair for the scheme. The uppercase spelling wins,
// matching net/http.
//
// HTTP_PROXY is deliberately not consulted from the CGI-ish lowercase
// http_proxy for https requests, because that is how net/http behaves and the
// difference is observable.
func proxyEnv(secure bool) string {
	if secure {
		if v := os.Getenv("HTTPS_PROXY"); v != "" {
			return v
		}
		return os.Getenv("https_proxy")
	}
	if v := os.Getenv("HTTP_PROXY"); v != "" {
		return v
	}
	return os.Getenv("http_proxy")
}

// parseProxyURL accepts the forms people actually put in these variables,
// including a bare host:port with no scheme.
func parseProxyURL(raw string) (*proxy, error) {
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	switch u.Scheme {
	case "http":
	case "https", "socks5", "socks5h":
		return nil, ErrProxyScheme
	default:
		return nil, ErrProxyScheme
	}
	if u.Host == "" {
		return nil, ErrProxyScheme
	}

	host, port := u.Hostname(), u.Port()
	if port == "" {
		port = "80"
	}
	p := &proxy{Host: host, Port: port}
	if u.User != nil {
		pass, _ := u.User.Password()
		p.Auth = "Basic " + basicAuth(u.User.Username(), pass)
	}
	return p, nil
}

// matchNoProxy reports whether NO_PROXY exempts this destination.
//
// The rules follow net/http: a bare "*" disables proxying entirely, an entry
// may carry a port, and a leading dot or a plain suffix both match subdomains.
// IP addresses are compared literally; CIDR entries are not supported, which is
// the one deliberate simplification.
func matchNoProxy(host, port string) bool {
	raw := os.Getenv("NO_PROXY")
	if raw == "" {
		raw = os.Getenv("no_proxy")
	}
	if raw == "" {
		return false
	}

	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.ToLower(strings.TrimSpace(entry))
		if entry == "" {
			continue
		}
		if entry == "*" {
			return true
		}
		// An entry may pin a port, in which case both must match.
		if h, p, err := net.SplitHostPort(entry); err == nil && p != "" {
			if p != port {
				continue
			}
			entry = h
		}
		entry = strings.TrimSuffix(entry, ".")
		if entry == "" {
			continue
		}
		if strings.HasPrefix(entry, ".") {
			if strings.HasSuffix(host, entry) {
				return true
			}
			// ".example.com" also exempts "example.com" itself.
			if host == entry[1:] {
				return true
			}
			continue
		}
		if host == entry || strings.HasSuffix(host, "."+entry) {
			return true
		}
	}
	return false
}

// basicAuth base64-encodes credentials without importing encoding/base64's
// larger surface. The alphabet is the standard one with padding.
func basicAuth(user, pass string) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	src := user + ":" + pass
	var sb strings.Builder
	for i := 0; i < len(src); i += 3 {
		var chunk [3]byte
		n := copy(chunk[:], src[i:])
		sb.WriteByte(alphabet[chunk[0]>>2])
		sb.WriteByte(alphabet[(chunk[0]&0x03)<<4|chunk[1]>>4])
		if n > 1 {
			sb.WriteByte(alphabet[(chunk[1]&0x0f)<<2|chunk[2]>>6])
		} else {
			sb.WriteByte('=')
		}
		if n > 2 {
			sb.WriteByte(alphabet[chunk[2]&0x3f])
		} else {
			sb.WriteByte('=')
		}
	}
	return sb.String()
}
