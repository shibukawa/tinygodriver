---
id: requirement:http-proxy-support
type: requirement
title: HTTP Proxy Support
---
The native path reads the proxy environment and tunnels with CONNECT, so a program behaves the same whichever compiler produced it.

```yaml
priority: should
state: implemented
found: 2026-07-30, by a user on a corporate windows network
original_gap: >
  the native path dialled the origin directly and consulted nothing. grep -i
  proxy over the non-test sources of https/ and netdev/ returned nothing at
  all, while roundtrip_std.go had proxy support for free because it clones
  http.DefaultTransport, which carries Proxy: ProxyFromEnvironment. The result
  was that a plain go build worked behind a proxy and the same program built
  with -tags force_tinygo_logic did not, which reads as a bug in the tagged
  build rather than a missing feature.
implementation:
  files: https/proxy.go, https/proxy_native.go, https/proxy_unsupported.go
  variables: HTTPS_PROXY, HTTP_PROXY, NO_PROXY, plus the lowercase spellings
  precedence: uppercase first, matching net/http
  https_flow: DialPlain to the proxy, CONNECT, then Upgrade on the same socket
  http_flow: dial the proxy and write the absolute request form via WriteProxy
  auth: userinfo in the proxy URL becomes Proxy-Authorization Basic
  no_backend_changes: >
    api:tls-dialer already exposed the two primitives CONNECT needs, so nothing
    below the client had to move. This is what made the feature cheap.
  darwin_dividend: >
    the proxied path goes through Upgrade, so it uses Secure Transport rather
    than Network.framework, which cannot adopt an existing socket. It therefore
    works on darwin at all, at the TLS 1.2 ceiling the STARTTLS path documents.
refused:
  schemes: [https, socks5, socks5h]
  sentinel: ErrProxyScheme
  reason: >
    an https:// proxy means TLS inside TLS, and every native backend starts
    from a descriptor, so the inner session has no socket to use. Failing
    loudly beats connecting direct, which would send traffic a deployment
    expects to be proxied.
  future: >
    all three backends are really buffer transformers -- schannel never sees
    the socket at all -- so taking read and write callbacks instead of a
    descriptor would unlock this. Network.framework could not follow, but it
    already sits out the upgrade path.
verified:
  how: >
    a hand-rolled CONNECT proxy in proxy_tunnel_test.go, reading its request
    head one byte at a time so the test exercises the same discipline the
    client needs
  covers:
    - a real end-to-end TLS session through the tunnel, against an origin with its own CA
    - the origin certificate is verified, not the proxy's
    - NO_PROXY bypasses the tunnel and connects direct
    - Proxy-Authorization is sent
    - a 407 is reported as an authentication failure
    - plain http uses the absolute request form
  platforms: >
    passes on darwin; on windows under wine everything passes except the cases
    needing custom-CA acceptance, which wine's crypt32 cannot do at all. The
    failure messages name "upgrade" and "dial" respectively, which is itself
    evidence the tunnel and the NO_PROXY bypass worked.
not_covered:
  - NO_PROXY CIDR ranges; host names, suffixes, literal IPs and ports all work
windows_env_note: >
  windows environment variables are case-insensitive, so HTTPS_PROXY and
  https_proxy are one variable there and the precedence rule is unobservable.
  Checking both names stays harmless, and the test skips that assertion.
```
