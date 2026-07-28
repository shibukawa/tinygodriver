//go:build windows

package netdev

import (
	"encoding/pem"
	"errors"
	"sync"

	"github.com/shibukawa/tinygodriver/internal/schannel"
)

// The Netdever seam passes TLS state as a uintptr. A Go pointer cannot be
// stored in one across a cgo boundary, so sessions live in a table and the
// uintptr is an index into it.
var (
	sessionMu   sync.Mutex
	sessions            = map[uintptr]*schannel.Session{}
	sessionNext uintptr = 1
)

func sessionHandle(s *schannel.Session) uintptr {
	sessionMu.Lock()
	defer sessionMu.Unlock()
	h := sessionNext
	sessionNext++
	sessions[h] = s
	return h
}

func sessionFromHandle(h uintptr) *schannel.Session {
	sessionMu.Lock()
	defer sessionMu.Unlock()
	return sessions[h]
}

func releaseHandle(h uintptr) {
	sessionMu.Lock()
	defer sessionMu.Unlock()
	delete(sessions, h)
}

// pemToDER decodes every CERTIFICATE block. Decoding in Go keeps a base64
// decoder out of the C layer.
func pemToDER(data []byte) ([][]byte, error) {
	var out [][]byte
	rest := data
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type == "CERTIFICATE" {
			out = append(out, block.Bytes)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("tls: no CERTIFICATE block in SSL_CERT_FILE")
	}
	return out, nil
}
