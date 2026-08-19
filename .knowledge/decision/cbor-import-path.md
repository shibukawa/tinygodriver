---
id: decision:cbor-import-path
type: decision
title: CBOR Lands At encoding/cbor
---
The moved package lands at `encoding/cbor`, mirroring the standard library the way `compress/zstd` already does, rather than flat at the repository root.

```yaml
state: accepted 2026-08-19, implemented
state: proposed
proposed_on: 2026-08-19
import_path: github.com/shibukawa/tinygodriver/encoding/cbor
package_name: cbor
proposed_by: the maintainer, when asking for requirement:cbor-package-move
precedent: >
  compress/zstd is already a stdlib-named group holding a stdlib-named leaf, and
  cloud, nosql, storage and database/sql are all group trees. Flat packages are
  the minority here.
rejected:
  flat_cbor: >
    argued from a reading that decision:package-layout makes the root flat. It
    does not. That decision is about the https package and cites httpmux and
    httprevproxy as its two flat siblings; it is a per-package placement, not a
    repository rule.
  contrib_cbor: no contrib tree exists and one should not be created for this
  codec_cbor: >
    a codec/ group invented for one member. encoding/ is the name the standard
    library already uses for this category, and a second binary format would
    join it rather than force a rename.
opens: >
  nothing else is planned for encoding/. The name is chosen because it is
  already correct, not to reserve space.
```
