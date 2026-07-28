---
id: decision:postgres-tls-via-proxy
type: decision
title: PostgreSQL TLS Rides the Upgrade Seam
---
The TinyGo driver connects in plaintext and upgrades in place through requirement:tls-upgrade-seam; a terminating proxy is no longer the only supported deployment.

```yaml
state: accepted, superseding both the permanent exclusion and the proxy-only phase
why_it_became_possible:
  was: >
    postgres upgrades an already-connected socket, and the darwin backend owned
    the whole connection, so tls could not attach after connect
  now: >
    decision:darwin-hybrid-tls keeps Network.framework for plain dials and adds
    Secure Transport for adopting a live fd; linux mbedtls does the same
integration_point:
  file: pgconn/pgconn.go, func startTLS
  detail: >
    pgx already writes SSLRequest and reads the 'S' reply; only its final
    tls.Client call is replaced. One call site, inside the three files of
    decision:postgres-backend-split
  fd_supply: pgx Config.DialFunc returns a conn carrying its netdev fd
  reached_via: stdlib.OpenDB takes the pgx.ConnConfig that carries DialFunc
state_note: shipped 2026-07-28; sslmode works on darwin and linux
platform_state:
  darwin: available, TLS 1.2 via Secure Transport, 1.3 with -tags darwinstarttlswith13
  linux: available, TLS 1.3 via mbedtls
  windows: no upgradeTLS implementation, so sslmode stays unsupported there
  std_go: unaffected, system:pgx keeps full tls support
libpq_differences:
  verify_ca: >
    treated as verify-full; the native backends cannot skip the hostname check,
    and checking it is stricter, never weaker
  client_certs: sslcert and sslkey are rejected rather than ignored
behavior:
  sslmode_disable: supported everywhere
  sslmode_other:
    where_seam_exists: supported
    elsewhere: ErrTLSUnsupported at connect, never silent plaintext fallback
constraint: requirement:platform-matrix forbids silent plaintext fallback
proxy_still_valid: >
  terminating tls at a proxy remains supported and is the simplest deployment;
  it is no longer a requirement
tls_version_note: >
  the darwin upgrade path is Secure Transport, measured at TLS 1.2 maximum even
  when 1.3 is requested; the plain dial path keeps 1.3 via Network.framework
