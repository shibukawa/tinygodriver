//go:build tinygo || force_tinygo_logic

package httpserver

import (
	"bufio"
	"bytes"
	"net/http"
	"testing"
)

// TestRawHeadHasUpgradeMatchesParsedPredicate locks the raw byte scan to the
// answer the default predicate gives after a full parse: for every head that
// http.ReadRequest accepts, rawHeadHasUpgrade must agree with IsUpgrade.
func TestRawHeadHasUpgradeMatchesParsedPredicate(t *testing.T) {
	cases := []struct {
		name string
		head string
	}{
		{"no connection header", "GET / HTTP/1.1\r\nHost: h\r\n\r\n"},
		{"plain upgrade", "GET / HTTP/1.1\r\nHost: h\r\nConnection: upgrade\r\n\r\n"},
		{"mixed case value", "GET / HTTP/1.1\r\nHost: h\r\nConnection: UpGrAdE\r\n\r\n"},
		{"mixed case name", "GET / HTTP/1.1\r\nHost: h\r\ncOnNeCtIoN: upgrade\r\n\r\n"},
		{"keep-alive only", "GET / HTTP/1.1\r\nHost: h\r\nConnection: keep-alive\r\n\r\n"},
		{"keep-alive comma upgrade", "GET / HTTP/1.1\r\nHost: h\r\nConnection: keep-alive,upgrade\r\n\r\n"},
		{"keep-alive space upgrade", "GET / HTTP/1.1\r\nHost: h\r\nConnection: keep-alive,  upgrade\r\n\r\n"},
		{"upgrade then keep-alive", "GET / HTTP/1.1\r\nHost: h\r\nConnection: upgrade, keep-alive\r\n\r\n"},
		{"tabs around token", "GET / HTTP/1.1\r\nHost: h\r\nConnection: keep-alive,\tupgrade\t\r\n\r\n"},
		{"trailing spaces", "GET / HTTP/1.1\r\nHost: h\r\nConnection: upgrade   \r\n\r\n"},
		{"token with suffix", "GET / HTTP/1.1\r\nHost: h\r\nConnection: upgraded\r\n\r\n"},
		{"token with prefix", "GET / HTTP/1.1\r\nHost: h\r\nConnection: xupgrade\r\n\r\n"},
		{"truncated token", "GET / HTTP/1.1\r\nHost: h\r\nConnection: upgrad\r\n\r\n"},
		{"internal space", "GET / HTTP/1.1\r\nHost: h\r\nConnection: up grade\r\n\r\n"},
		{"empty elements", "GET / HTTP/1.1\r\nHost: h\r\nConnection: , upgrade ,\r\n\r\n"},
		{"only commas", "GET / HTTP/1.1\r\nHost: h\r\nConnection: ,,,\r\n\r\n"},
		{"repeated header second matches", "GET / HTTP/1.1\r\nHost: h\r\nConnection: keep-alive\r\nConnection: upgrade\r\n\r\n"},
		{"repeated header no match", "GET / HTTP/1.1\r\nHost: h\r\nConnection: keep-alive\r\nConnection: close\r\n\r\n"},
		{"upgrade header without connection", "GET / HTTP/1.1\r\nHost: h\r\nUpgrade: websocket\r\n\r\n"},
		{"similar header name", "GET / HTTP/1.1\r\nHost: h\r\nX-Connection: upgrade\r\n\r\n"},
		{"connection prefix name", "GET / HTTP/1.1\r\nHost: h\r\nConnection-Id: upgrade\r\n\r\n"},
		{"value on other header", "GET / HTTP/1.1\r\nHost: h\r\nAccept: upgrade\r\nConnection: close\r\n\r\n"},
		{"upgrade token in body", "POST / HTTP/1.1\r\nHost: h\r\nContent-Length: 20\r\n\r\nConnection: upgrade\n"},
		{"folded continuation carries token", "GET / HTTP/1.1\r\nHost: h\r\nConnection: keep-alive,\r\n upgrade\r\n\r\n"},
		{"folded continuation splits token", "GET / HTTP/1.1\r\nHost: h\r\nConnection: upg\r\n rade\r\n\r\n"},
		{"folded continuation after token", "GET / HTTP/1.1\r\nHost: h\r\nConnection: upgrade\r\n x\r\n\r\n"},
		{"folded other header absence", "GET / HTTP/1.1\r\nHost: h\r\nX-Other: a\r\n Connection: upgrade\r\nConnection: close\r\n\r\n"},
		{"folded other header then match", "GET / HTTP/1.1\r\nHost: h\r\nX-Other: a\r\n b\r\nConnection: upgrade\r\n\r\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			head := []byte(tc.head)
			req, err := http.ReadRequest(bufio.NewReader(bytes.NewReader(head)))
			if err != nil {
				t.Fatalf("http.ReadRequest rejected the head: %v", err)
			}
			want := IsUpgrade(req)
			got := rawHeadHasUpgrade(head)
			if got != want {
				t.Errorf("rawHeadHasUpgrade = %v, IsUpgrade on the parsed request = %v\nhead: %q\nparsed Connection: %q",
					got, want, tc.head, req.Header["Connection"])
			}
		})
	}
}
