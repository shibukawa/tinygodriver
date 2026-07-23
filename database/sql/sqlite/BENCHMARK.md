# SQLite backend baseline

Baseline recorded on 2026-07-17 with Go 1.26.4, macOS/arm64, and an Apple M3.
`BenchmarkInsertAndQuery` performs one prepared in-memory insert followed by one
prepared query. Results are three 500 ms samples, so they are a regression
reference rather than a cross-machine guarantee.

| Backend | ns/op samples | Test binary size |
| --- | --- | ---: |
| mattn | 2398, 2241, 2186 | 8,261,794 bytes |
| Petitweb tinygosqlite | 2177, 2382, 2251 | 7,507,650 bytes |
| modernc | 2647, 2636, 2610 | 10,676,274 bytes |

The test-binary sizes include the Go test runtime and fixtures. Reproduce the
latency measurement with:

```sh
go test ./database/sql/sqlite -run '^$' -bench BenchmarkInsertAndQuery -benchtime=500ms -count=3
go test -tags force_tinygo_logic ./database/sql/sqlite -run '^$' -bench BenchmarkInsertAndQuery -benchtime=500ms -count=3
CGO_ENABLED=0 go test ./database/sql/sqlite -run '^$' -bench BenchmarkInsertAndQuery -benchtime=500ms -count=3
```
