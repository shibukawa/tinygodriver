//go:build (tinygo || force_tinygo_logic) && !wasip1 && !wasip2 && !nosqlite

package sqlite

const expectedBackend = "tinygosqlite"
