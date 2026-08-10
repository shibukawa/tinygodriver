//go:build !tinygo

package websocket

// tlsDialError is what a wss:// dial is expected to return. Standard Go really
// attempts the handshake, so the error depends on what answers; nil here means
// the test only requires that some error came back.
func tlsDialError() error { return nil }
