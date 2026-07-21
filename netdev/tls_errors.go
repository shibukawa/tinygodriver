//go:build darwin || linux

package netdev

import "errors"

func tlsError(code int) error {
	switch {
	case code == -101:
		return errors.New("tls: failed to allocate client state")
	case code == -102:
		return errors.New("tls: handshake setup or certificate verification failed")
	case code <= -200 && code > -300:
		return errors.New("tls: handshake failed")
	default:
		return errors.New("tls: encrypted I/O failed")
	}
}
