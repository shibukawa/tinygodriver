---
id: api:https-transport
type: api
title: HTTPS Transport
---
`Transport` implements `http.RoundTripper` by obtaining a TLS `net.Conn` from api:tls-dialer, writing the request, and parsing the response.

```yaml
type: |
  type Transport struct {
      Config              *Config
      DialTimeout         time.Duration
      ResponseTimeout     time.Duration
      MaxIdleConnsPerHost int
      IdleConnTimeout     time.Duration
      DisableKeepAlives   bool
  }
methods:
  - func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error)
  - func (t *Transport) CloseIdleConnections()
  - func NewTransport(opts ...Option) *Transport
  - var DefaultTransport *Transport
implementation:
  std_go: delegates to a configured net/http.Transport; see requirement:std-go-delegation
  tinygo: |
    conn := pooled connection for the destination, else dialTLS(host, port, cfg)
    req.Write(conn)
    resp := http.ReadResponse(conn's own bufio.Reader, req)
    resp.Body wraps conn and on Close either pools it or closes it
    see requirement:connection-reuse and flow:connection-lease
roundtripper_contract:
  - never modify req
  - always close req.Body
  - return response with Body non-nil even when empty
  - errors returned unwrapped by http.Client, so they must satisfy requirement:error-classification
config_precedence: "Transport.Config, else DefaultConfig"
pooling: >
  the three idle-connection fields mean the same thing on both paths and are
  forwarded to net/http.Transport in std go builds
detail: flow:https-roundtrip
