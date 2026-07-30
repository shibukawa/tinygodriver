---
id: requirement:windows-dns-resolution
type: requirement
title: Windows Resolver Is Hardcoded to 8.8.8.8
---
netdev never reads the DNS servers Windows has configured, so any name that only an internal resolver can answer is unresolvable.

```yaml
priority: must
state: gap, not implemented
found: 2026-07-30, by a user trying to reach an internal https server
mechanism:
  - paths_windows.go resolvPath() returns ""
  - dns.go nameservers() does os.ReadFile(""), which always fails
  - the list is therefore always empty and the 8.8.8.8 fallback always wins
  - no environment variable overrides it; netdev reads only SystemRoot and SSL_CERT_FILE
consequence:
  split_horizon: internal names fail with ErrHostUnknown
  privacy: every lookup on windows is sent to a public resolver by default
  scope: windows only. linux and darwin both have a real /etc/resolv.conf.
workaround:
  what: add the host to %SystemRoot%\System32\drivers\etc\hosts
  why_it_works: lookupHostsFile runs before nameservers() and hostsPath() is correct on windows
fix_options:
  dnsapi_preferred:
    call: DnsQuery_W from dnsapi.dll
    why: >
      it delegates the whole lookup to the system resolver, so the DNS suffix
      search list, NRPT policy and any DoH configuration all apply. A corporate
      short name often needs the search list, which a bare server list would
      not supply.
    tinygo: reachable through cgo, the same way netdev already reaches winsock
    nocgo: reachable through syscall.NewLazyDLL on host Go
  getadaptersaddresses:
    call: GetAdaptersAddresses from iphlpapi.dll
    why_not: >
      yields the server addresses but not the search list or the policy table,
      so it fixes fully qualified internal names and not much else
  net_lookuphost:
    scope: the !cgo host-Go backend only
    note: >
      safe there because register_std.go makes useNetdev a no-op, so net does
      not route back into netdev. Under tinygo it would recurse forever.
related: requirement:http-proxy-support, found in the same session on the same network
```
