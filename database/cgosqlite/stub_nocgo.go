//go:build !cgo

// Package cgosqlite requires cgo. It remains importable in no-cgo package
// discovery builds so that the sqlite facade can select another backend.
package cgosqlite

const DriverName = "sqlite"
