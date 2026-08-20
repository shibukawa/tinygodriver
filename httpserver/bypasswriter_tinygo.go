//go:build tinygo || force_tinygo_logic

package httpserver

import (
	"bufio"
	"net"
	"net/http"
	"strconv"
)

// bypassWriter is the smallest ResponseWriter that also satisfies
// http.Hijacker, which is all an upgrade handler asks for. It exists to be
// hijacked: everything it writes is what a handler emits when it decides not to
// upgrade after all, such as the 403 an origin check produces.
//
// The body is buffered so the response can carry an accurate Content-Length.
// That keeps a client from waiting on a body that never ends, and it is why
// this writer offers no Flush: a handler that streams is not an upgrade
// handler.
type bypassWriter struct {
	conn net.Conn
	br   *bufio.Reader
	bw   *bufio.Writer
	hdr  http.Header

	body     []byte
	code     int
	wroteHdr bool
	hijacked bool
}

func (w *bypassWriter) Header() http.Header { return w.hdr }

func (w *bypassWriter) WriteHeader(code int) {
	if w.wroteHdr {
		return
	}
	w.wroteHdr = true
	w.code = code
}

func (w *bypassWriter) Write(b []byte) (int, error) {
	if w.hijacked {
		return 0, http.ErrHijacked
	}
	if !w.wroteHdr {
		w.WriteHeader(http.StatusOK)
	}
	w.body = append(w.body, b...)
	return len(b), nil
}

// Hijack hands the connection to the handler. Unlike net/http's, it cannot
// fail: nothing is reading the connection behind the handler's back, so there
// is no background read to abort and nothing to wait for.
func (w *bypassWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if w.hijacked {
		return nil, nil, http.ErrHijacked
	}
	w.hijacked = true
	return w.conn, bufio.NewReadWriter(w.br, w.bw), nil
}

// finish emits the buffered response. A handler that hijacked never reaches it.
func (w *bypassWriter) finish() {
	if !w.wroteHdr {
		w.WriteHeader(http.StatusOK)
	}
	writeStatusLine(w.bw, w.code)
	writeHeaders(w.bw, len(w.body), w.hdr)
	w.bw.Write(w.body)
	w.bw.Flush()
}

func writeStatusLine(bw *bufio.Writer, code int) {
	bw.WriteString("HTTP/1.1 ")
	bw.WriteString(strconv.Itoa(code))
	bw.WriteString(" ")
	bw.WriteString(http.StatusText(code))
	bw.WriteString("\r\n")
}

// writeHeaders emits hdr with an accurate Content-Length, and closes the
// connection afterwards: this path serves one request and then stops, so
// announcing anything else would strand the client. The two fields this
// function owns go straight to the writer rather than through the map, which
// keeps the pre-handler error responses from building a header map at all.
func writeHeaders(bw *bufio.Writer, bodyLen int, hdr http.Header) {
	if hdr != nil {
		hdr.Del("Content-Length")
		hdr.Del("Connection")
		hdr.Write(bw)
	}
	bw.WriteString("Content-Length: ")
	bw.WriteString(strconv.Itoa(bodyLen))
	bw.WriteString("\r\nConnection: close\r\n\r\n")
}
