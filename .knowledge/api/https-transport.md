---
id: api:https-transport
type: api
title: HTTPS Transport
---
`Transport` implements `http.RoundTripper` by obtaining a TLS `net.Conn` from api:tls-dialer, writing the request, and parsing the response.

```yaml
type: |
  type Transport struct {
      Config          *Config
      DialTimeout     time.Duration
      ResponseTimeout time.Duration
  }
methods:
  - func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error)
  - func NewTransport(opts ...Option) *Transport
  - var DefaultTransport *Transport
implementation:
  std_go: delegates to a configured net/http.Transport; see requirement:std-go-delegation
  tinygo: |
    conn := dialTLS(host, port, cfg)
    req.Write(conn)
    resp := http.ReadResponse(bufio.NewReader(conn), req)
    resp.Body wraps conn and closes it on Close
roundtripper_contract:
  - never modify req
  - always close req.Body
  - return response with Body non-nil even when empty
  - errors returned unwrapped by http.Client, so they must satisfy requirement:error-classification
config_precedence: "Transport.Config, else DefaultConfig"
detail: flow:https-roundtrip
