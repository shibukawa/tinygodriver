//go:build tinygo || force_tinygo_logic

package httprevproxy

import (
	"errors"
	"net/http"
)

// fixOutboundHost compensates for a TinyGo/standard-Go difference that decides
// where the proxied request is actually sent.
//
// Standard net/http dials Request.URL.Host and sends Request.Host as the Host
// header, so the two are independent. TinyGo's transport takes the dial address
// from Request.Host (src/net/http/client.go, roundTrip), which collapses them
// into one field.
//
// Rewrite plus ProxyRequest.SetURL leaves Host empty, which is how net/http
// spells "take the Host header from the URL". TinyGo reads that as an empty
// dial address and fails with "invalid IP address". Filling it in from the URL
// is exactly what standard net/http would have written on the wire, so the
// request is unchanged on both compilers.
//
// A Host that disagrees with the URL cannot be honored at all here: TinyGo
// would dial the Host and ignore the URL. That is what Director does, so
// NewSingleHostReverseProxy sends the proxy back to its own address and loops
// until the header limit trips. There is no correct answer to pick, so this
// reports instead of guessing.
func fixOutboundHost(outreq *http.Request) error {
	if outreq.URL == nil || outreq.URL.Host == "" {
		return nil
	}
	if outreq.Host == "" {
		outreq.Host = outreq.URL.Host
		return nil
	}
	if outreq.Host != outreq.URL.Host {
		return errors.New("httprevproxy: TinyGo dials Request.Host, so it cannot differ from " +
			"the target URL host (" + outreq.Host + " vs " + outreq.URL.Host + "); use Rewrite " +
			"with SetURL, or set Out.Host to the target, instead of Director")
	}
	return nil
}
