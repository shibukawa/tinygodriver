---
id: decision:fasthttp-zstd-backend
type: decision
title: fasthttp zstd Backend Under TinyGo
---
Compress zstd through the repository's own compress/zstd in TinyGo builds of decision:fasthttp-fork instead of klauspost/compress/zstd, so that a plain `tinygo build` links. Accept losing zstd decoding, which compress/zstd does not implement.

```yaml
state: shipped 2026-08-11
problem: >
  klauspost/compress/zstd decodes in hand-written asm (buildDtable_asm plus five
  sequenceDecs_*_arm64). tinygo links none of it, so upstream fasthttp fails at
  LINK time after a clean compile. -tags noasm was the previous answer.
file_split: follows rule:build-tag-selection
  zstd.go: "!fasthttp_nozstd && !tinygo && !force_tinygo_logic; klauspost, encode+decode+7 levels, upstream code"
  zstd_tinygo.go: "!fasthttp_nozstd && (tinygo || force_tinygo_logic); compress/zstd, encode only"
  zstd_disabled.go: "fasthttp_nozstd; neither"
gates:
  zstdAvailable: can encode; gates both CompressHandler switches in server.go
  zstdDecodeAvailable: >
    can decode; new. Gates fs.go compressZstd and the two FS CompressZstd
    defaults, because newFSFile reads a compressed file back to sniff a content
    type when the extension has no MIME entry. An encode-only build would answer
    application/octet-stream for the zstd representation and a sniffed type for
    the identity one, and Content-Type must not vary with Accept-Encoding.
    Flipping this constant re-enables FS zstd if compress/zstd ever decodes.
sizes_darwin_arm64_tinygo_0_41_1: minimal two-route server
  net_http: 1.22 MB
  fork_no_tags: 2.83 MB
  fork_fasthttp_nozstd: 2.78 MB
  fork_before_noasm: 5.32 MB
  zstd_cost: 0.05 MB now, 2.53 MB through klauspost
gives_up:
  decoding: BodyUnzstd, WriteUnzstd, AppendUnzstdBytes report ErrZstdUnsupported; a client should ask for br or gzip, or call klauspost itself on resp.Body() under -tags noasm
  levels: compress/zstd has one; CompressZstd* constants and level params are normalized then ignored, so app code compiles either way; per-level pool maps collapse to one pool per writer kind
  fs_zstd: FSHandler serves br and gzip only, FS.CompressZstd ignored
keeps: CompressHandler, CompressHandlerBrotliLevel, Response.zstdBody, SetBodyStreamWriter — the dynamic-response case
noasm_tag: now inert; no tinygo build reaches klauspost zstd
fasthttp_nozstd_tag: kept, but its payoff fell from half the binary to 0.05 MB
err_zstd_unsupported: declared in all three files, including the std-Go one that never returns it, so errors.Is compiles under every tag combination
upstream_change: >
  compress/zstd gained (*Writer).Reset(io.Writer) in both implementations for
  this. fasthttp pools encoders; without Reset every response would build a new
  128 KiB block buffer and 16 KiB match table, which is nearly the whole
  encoder. Reset keeps those and the WithETag setting, and still defers the
  frame header.
verified:
  - tinygo build and tinygo test ./fasthttp/ with no tags, previously a link error
  - go test x4 tag combinations, tinygo test x2, compress/zstd both ways
  - >
    zstd_wire_test.go decodes with klauspost what compress/zstd put on the wire
    through the pooled stackless writer, under -tags force_tinygo_logic; this is
    the only way to check the encoder, since tinygo cannot decode
  - vendor.py re-run reproduces the tree byte for byte
```
