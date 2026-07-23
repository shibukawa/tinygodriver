//go:build !cgo

// Package tinygosqlite requires cgo. It remains importable in no-cgo package
// discovery builds so that the sqlite facade can select another backend.
package tinygosqlite

const DriverName = "sqlite"
