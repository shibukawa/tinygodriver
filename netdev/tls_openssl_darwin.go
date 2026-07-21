//go:build darwin

package netdev

/*
#cgo LDFLAGS: -L/opt/homebrew/lib -L/usr/local/lib -lssl -lcrypto
#include "tls_openssl.h"
*/
import "C"

import (
	"errors"
	"sync"
	"unsafe"
)

var tlsConnectMu sync.Mutex

func sysTLSConnect(fd int, hostname string) (uintptr, error) {
	if hostname == "" {
		return 0, errors.New("tls: empty server name")
	}
	name := append([]byte(hostname), 0)
	var state C.uintptr_t
	tlsConnectMu.Lock()
	rc := int(C.netdev_tls_connect(C.int(fd), (*C.char)(unsafe.Pointer(&name[0])), &state))
	tlsConnectMu.Unlock()
	if rc != 0 {
		return 0, tlsError(rc)
	}
	return uintptr(state), nil
}

func sysTLSSend(state uintptr, buf []byte) (int, error) {
	if state == 0 || len(buf) == 0 {
		return 0, nil
	}
	var n C.int
	rc := int(C.netdev_tls_write(C.uintptr_t(state), unsafe.Pointer(&buf[0]), C.int(len(buf)), &n))
	if rc != 0 {
		return -1, tlsError(rc)
	}
	return int(n), nil
}

func sysTLSRecv(state uintptr, buf []byte) (int, error) {
	if state == 0 || len(buf) == 0 {
		return 0, nil
	}
	var n C.int
	rc := int(C.netdev_tls_read(C.uintptr_t(state), unsafe.Pointer(&buf[0]), C.int(len(buf)), &n))
	if rc != 0 {
		return -1, tlsError(rc)
	}
	return int(n), nil
}

func sysTLSClose(state uintptr) {
	if state != 0 {
		C.netdev_tls_close(C.uintptr_t(state))
	}
}
