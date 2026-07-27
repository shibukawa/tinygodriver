package https

import (
	"io"
	"net/http"
	"net/url"
	"strings"
)

// DefaultClient is an http.Client using DefaultTransport.
var DefaultClient = &http.Client{Transport: DefaultTransport}

// NewClient returns an http.Client whose Transport applies the given options.
func NewClient(opts ...Option) *http.Client {
	return &http.Client{Transport: NewTransport(opts...)}
}

// Get issues a GET request, mirroring net/http.Get.
//
// An error is returned if the request could not be made. Any returned response
// has a non-nil Body which the caller must close.
func Get(url string) (*http.Response, error) {
	return DefaultClient.Get(url)
}

// Head issues a HEAD request, mirroring net/http.Head.
func Head(url string) (*http.Response, error) {
	return DefaultClient.Head(url)
}

// Post issues a POST request, mirroring net/http.Post.
func Post(url, contentType string, body io.Reader) (*http.Response, error) {
	return DefaultClient.Post(url, contentType, body)
}

// PostForm issues a POST with data URL-encoded as the request body, mirroring
// net/http.PostForm.
func PostForm(url string, data url.Values) (*http.Response, error) {
	return DefaultClient.PostForm(url, data)
}

// hostPort splits a URL host into a host and a port, defaulting the port from
// the scheme.
func hostPort(u *url.URL) (string, string) {
	host := u.Host
	port := ""
	if i := strings.LastIndex(host, ":"); i != -1 && !strings.Contains(host[i+1:], "]") {
		host, port = host[:i], host[i+1:]
	}
	host = strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
	if port == "" {
		if u.Scheme == "http" {
			port = "80"
		} else {
			port = "443"
		}
	}
	return host, port
}
