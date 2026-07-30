//go:build tinygo || force_tinygo_logic

package https

import (
	"errors"
	"runtime"
	"testing"
)

func TestProxyEnvSelection(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://secure:8443")
	t.Setenv("HTTP_PROXY", "http://plain:8080")
	t.Setenv("NO_PROXY", "")

	p, err := proxyFor("example.com", "443", true)
	if err != nil || p == nil || p.Host != "secure" || p.Port != "8443" {
		t.Fatalf("https request: got %+v, %v", p, err)
	}
	p, err = proxyFor("example.com", "80", false)
	if err != nil || p == nil || p.Host != "plain" || p.Port != "8080" {
		t.Fatalf("http request: got %+v, %v", p, err)
	}
}

// The uppercase spelling wins, matching net/http.
//
// Windows is excluded because its environment is case-insensitive: setting
// https_proxy there overwrites HTTPS_PROXY rather than sitting alongside it, so
// the two spellings are one variable and there is no precedence to observe.
// Checking both names stays harmless on that platform.
func TestProxyEnvPrecedence(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows environment variables are case-insensitive")
	}
	t.Setenv("HTTPS_PROXY", "http://upper:1")
	t.Setenv("https_proxy", "http://lower:2")
	t.Setenv("NO_PROXY", "")

	p, _ := proxyFor("example.com", "443", true)
	if p == nil || p.Host != "upper" {
		t.Fatalf("got %+v, want upper", p)
	}
}

func TestProxyURLForms(t *testing.T) {
	cases := []struct {
		raw, host, port, auth string
		wantErr               bool
	}{
		{raw: "http://p:3128", host: "p", port: "3128"},
		// A bare host:port is common in these variables and has no scheme.
		{raw: "p:3128", host: "p", port: "3128"},
		{raw: "http://p", host: "p", port: "80"},
		{raw: "http://u:pw@p:3128", host: "p", port: "3128", auth: "Basic dTpwdw=="},
		// TLS to the proxy would need TLS inside TLS, which no backend can do.
		{raw: "https://p:3128", wantErr: true},
		{raw: "socks5://p:1080", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			p, err := parseProxyURL(tc.raw)
			if tc.wantErr {
				if !errors.Is(err, ErrProxyScheme) {
					t.Fatalf("err = %v, want ErrProxyScheme", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if p.Host != tc.host || p.Port != tc.port {
				t.Fatalf("got %s:%s, want %s:%s", p.Host, p.Port, tc.host, tc.port)
			}
			if p.Auth != tc.auth {
				t.Fatalf("auth = %q, want %q", p.Auth, tc.auth)
			}
		})
	}
}

func TestNoProxyMatching(t *testing.T) {
	cases := []struct {
		noProxy, host, port string
		want                bool
	}{
		{"", "example.com", "443", false},
		{"*", "example.com", "443", true},
		{"example.com", "example.com", "443", true},
		{"example.com", "www.example.com", "443", true},
		{"example.com", "notexample.com", "443", false},
		{".example.com", "www.example.com", "443", true},
		// A leading dot still exempts the bare domain, as net/http does.
		{".example.com", "example.com", "443", true},
		{"a.com,b.com", "b.com", "443", true},
		{" a.com , b.com ", "b.com", "443", true},
		{"example.com:8080", "example.com", "8080", true},
		{"example.com:8080", "example.com", "443", false},
		{"10.0.0.1", "10.0.0.1", "443", true},
		{"EXAMPLE.COM", "example.com", "443", true},
		{"example.com", "example.com.", "443", true},
	}
	for _, tc := range cases {
		t.Setenv("NO_PROXY", tc.noProxy)
		if got := matchNoProxy(tc.host, tc.port); got != tc.want {
			t.Errorf("NO_PROXY=%q host=%s port=%s: got %v, want %v",
				tc.noProxy, tc.host, tc.port, got, tc.want)
		}
	}
}

// NO_PROXY must win over a configured proxy.
func TestNoProxyOverridesProxy(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://p:3128")
	t.Setenv("NO_PROXY", "internal.example.com")

	p, err := proxyFor("internal.example.com", "443", true)
	if err != nil || p != nil {
		t.Fatalf("got %+v, %v; want a direct connection", p, err)
	}
	p, err = proxyFor("external.example.com", "443", true)
	if err != nil || p == nil {
		t.Fatalf("got %+v, %v; want the proxy", p, err)
	}
}

func TestBasicAuthEncoding(t *testing.T) {
	// Lengths 1..4 cover every padding case in the 3-byte encoder.
	cases := map[string]string{
		"a:":     "YTo=",
		"u:pw":   "dTpwdw==",
		"ab:cd":  "YWI6Y2Q=",
		"abc:de": "YWJjOmRl",
	}
	for in, want := range cases {
		user, pass, _ := cut(in, ":")
		if got := basicAuth(user, pass); got != want {
			t.Errorf("basicAuth(%q) = %q, want %q", in, got, want)
		}
	}
}

func cut(s, sep string) (string, string, bool) {
	for i := 0; i+len(sep) <= len(s); i++ {
		if s[i:i+len(sep)] == sep {
			return s[:i], s[i+len(sep):], true
		}
	}
	return s, "", false
}
