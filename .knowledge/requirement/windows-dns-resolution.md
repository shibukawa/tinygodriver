---
id: requirement:windows-dns-resolution
type: requirement
title: Resolver Discovery and the NETDEV_DNS Override
---
What netdev can discover is not always what the machine actually uses, so NETDEV_DNS overrides the resolver list on every platform.

```yaml
priority: must
state: mitigated by an override; system integration still absent
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
remaining_work:
  windows:
    call: DnsQuery_W from dnsapi.dll
    why: >
      it delegates the whole lookup to the system resolver, so the DNS suffix
      search list, NRPT policy and any DoH configuration apply. A corporate
      short name usually needs the search list, which a server list alone
      cannot supply.
    tinygo: reachable through cgo, as netdev already reaches winsock
    nocgo: reachable through syscall.NewLazyDLL on host Go
  macos:
    call: the System Configuration framework, which scutil --dns reads
  not_done_because: >
    the override unblocks the reported case on every platform, and the
    system-integration work is per-platform and individually larger
related: requirement:http-proxy-support, found in the same session on the same network
```
