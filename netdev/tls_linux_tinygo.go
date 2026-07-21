//go:build linux && tinygo

package netdev

func sysTLSConnect(fd int, hostname string) (uintptr, error) {
	return 0, ErrProtocolNotSupported
}

func sysTLSSend(state uintptr, buf []byte) (int, error) {
	return -1, ErrProtocolNotSupported
}

func sysTLSRecv(state uintptr, buf []byte) (int, error) {
	return -1, ErrProtocolNotSupported
}

func sysTLSClose(state uintptr) {}
