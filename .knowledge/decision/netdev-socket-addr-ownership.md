---
id: decision:netdev-socket-addr-ownership
type: decision
title: How Far netdev Fixes Listener Addresses
---
netdev keeps the authoritative per-socket local address and exposes it through an accessor; making `net.Listener.Addr()` correct is left to an upstream tinygo patch.

```yaml
state: accepted
question: how far should netdev go so that a port 0 listener reports a real port
options:
  accessor_only:
    do: resolve laddr with getsockname after bind, expose it on Device
    cost: small, no new API surface for applications
    limit: net.Listener.Addr() still reports port 0
  netdev_listener:
    do: ship netdev.Listen returning a netdev-owned net.Listener
    cost: duplicates the tinygo net surface and puts two listener types in one program
    rejected_because: application code should stay on the standard net API
  upstream_patch:
    do: change tinygo listenTCP to re-read the bound address after Bind
    cost: external release cycle, no local control over timing
    status: the only complete fix
chosen: accessor_only now, upstream_patch tracked separately
rationale: >
  keep the driver surface equal to Netdever, fix what netdev actually owns, and
  do not fork the net API over one address field
affects: requirement:netdev-bound-port
```
