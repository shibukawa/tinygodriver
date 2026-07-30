---
id: requirement:windows-dns-resolution
type: requirement
title: Resolver Discovery and the NETDEV_DNS Override
---
What netdev can discover is not always what the machine actually uses, so NETDEV_DNS overrides the resolver list on every platform.

```yaml
priority: must
state: implemented where the linker allows it; see system_resolver
found: 2026-07-30, by a user who could not reach an internal https server
original_gap:
  windows:
    - paths_windows.go resolvPath() returns ""
    - dns.go nameservers() does os.ReadFile(""), which always fails
    - the list is therefore always empty and the 8.8.8.8 fallback always wins
    - nothing overrode it; netdev read only SystemRoot and SSL_CERT_FILE
  consequence:
    split_horizon: internal names failed with ErrHostUnknown and could not be fixed
    privacy: every lookup on windows went to a public resolver by default
override:
  env: NETDEV_DNS
  form: comma or space separated IPv4 addresses, optional :port defaulting to 53
  semantics: replaces the discovered list rather than extending it
  reason_for_replacing: >
    appending would leave a resolver that cannot answer internal names ahead of
    the one that can, which is the exact failure this exists to fix
  skips: unparseable and non-IPv4 entries, so one typo cannot void the list
  scope: all platforms, because the discovery problem is not windows-only
other_platforms:
  macos: >
    /etc/resolv.conf carries a header stating it is not consulted for
    resolution and directing the reader to scutil --dns. It happens to hold the
    right servers on a simple network, but it does not represent the
    domain-scoped resolvers a VPN installs, which scutil shows as separate
    resolver entries scoped by domain.
  linux: reads the real file; generally correct
  everywhere:
    - search and domain lines are ignored, so an unqualified short name never resolves
    - a resolver list of only IPv6 entries is treated as empty, since this
      package is IPv4-only, and silently becomes the 8.8.8.8 fallback
workaround_still_valid:
  what: an entry in /etc/hosts or %SystemRoot%\System32\drivers\etc\hosts
  why: lookupHostsFile runs before any resolver is consulted
system_resolver:
  order: localhost, hosts file, NETDEV_DNS, system resolver, built-in UDP query
  why_env_precedes_system: >
    an explicit override means the caller knows which resolver can answer, so
    falling back to the system one would silently bypass it
  implementations:
    host_go_any_os:
      call: net.LookupHost
      safe_because: >
        register_std.go makes useNetdev a no-op outside TinyGo, so net does not
        route back into this package. The same call under TinyGo would recurse.
    windows_cgo:
      call: getaddrinfo via ws2_32
      covers: TinyGo windows and host-Go windows with cgo
      why_getaddrinfo_over_dnsquery: >
        DnsQuery_W would need DNS_RECORD union parsing; getaddrinfo gives the
        same DNS Client service behaviour, including the suffix search list and
        NRPT, for a fraction of the surface
      layout_hazard: >
        ADDRINFOA is hand-declared because TinyGo compiles cgo without the
        system headers, and the windows layout is not the unix one:
        ai_canonname precedes ai_addr and ai_addrlen is a size_t. A wrong
        layout yields a plausible wrong address rather than a crash, which is
        why TestSystemLookupHostLocalhost asserts 127.0.0.1 specifically.
  not_implemented:
    builds: TinyGo on darwin and linux
    reason: linker, not design
    evidence:
      - "tinygo darwin: linking getaddrinfo fails with 'could not find symbol _getaddrinfo'"
      - "pointing the linker at a real SDK libSystem breaks the build instead"
      - "sys_linux.go states TinyGo's linux linker does not expose libc socket stubs"
    why_not_attempted: >
      a link failure is compile-time and cannot degrade gracefully, so adding
      the call would break a platform that currently works
    consequence: no search list and no domain-scoped resolvers on those builds
verified:
  darwin_host: system resolver used; localhost layout check passes
  windows_under_wine: >
    getaddrinfo path resolves, the hand-declared layout checks out against
    localhost, a real https.Get returns 200 OK, and NETDEV_DNS pointed at an
    unroutable server correctly fails instead of falling back
  tinygo_darwin: builds; takes the no-system-resolver path
related: requirement:http-proxy-support, found in the same session on the same network
```
