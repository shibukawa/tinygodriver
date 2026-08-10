// PETITWEB: added `&& !tinygo`. TinyGo has no http.NewResponseController, so it
// takes the pre-1.20 file's Hijacker path instead; see PATCHES.md.
//go:build (go1.20 || go1.21 || go1.22) && !tinygo

package websocket

import (
	"bufio"
	"net"
	"net/http"
)

func HijackResponse(_ *http.Request, w http.ResponseWriter) (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(w).Hijack()
}
