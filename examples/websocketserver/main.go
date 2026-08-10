// Command websocketserver shows a WebSocket endpoint living alongside ordinary
// HTTP routes on one port, under both compilers.
//
//	tinygo build -o wsserver ./examples/websocketserver
//	./wsserver
//	# open http://127.0.0.1:8080/ in a browser, or:
//	# curl http://127.0.0.1:8080/healthz
//
// go run ./examples/websocketserver works too. The only line that differs from
// an ordinary net/http program is the httpserver.Serve call, and under standard
// Go that call is http.Server.Serve.
package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/shibukawa/tinygodriver/httpserver"
	_ "github.com/shibukawa/tinygodriver/netdev"
	"github.com/shibukawa/tinygodriver/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:    4096,
	WriteBufferSize:   4096,
	EnableCompression: true,
}

// echo upgrades the connection and mirrors every message back, answering pings
// so an idle browser tab stays connected.
func echo(w http.ResponseWriter, r *http.Request) {
	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		// Upgrade has already written a response.
		log.Printf("upgrade from %s: %v", r.RemoteAddr, err)
		return
	}
	defer c.Close()
	log.Printf("connected: %s", r.RemoteAddr)

	c.SetReadLimit(1 << 20)
	for {
		mt, msg, err := c.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				log.Printf("closed: %s: %v", r.RemoteAddr, err)
			}
			return
		}
		if err := c.WriteMessage(mt, msg); err != nil {
			return
		}
	}
}

// clock pushes the time every second, so the example shows a server that talks
// first rather than only echoing.
func clock(w http.ResponseWriter, r *http.Request) {
	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer c.Close()

	// A reader must run for the connection to notice a close or a pong.
	go func() {
		for {
			if _, _, err := c.ReadMessage(); err != nil {
				c.Close()
				return
			}
		}
	}()
	for {
		msg := time.Now().Format(time.RFC3339)
		_ = c.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if err := c.WriteMessage(websocket.TextMessage, []byte(msg)); err != nil {
			return
		}
		time.Sleep(time.Second)
	}
}

const page = `<!doctype html>
<title>tinygodriver websocket</title>
<h1>echo</h1>
<input id="msg" value="hello"><button onclick="send()">send</button>
<pre id="log"></pre>
<script>
const ws = new WebSocket("ws://" + location.host + "/ws");
const log = (s) => document.getElementById("log").textContent += s + "\n";
ws.onopen = () => log("connected");
ws.onmessage = (e) => log("< " + e.data);
ws.onclose = () => log("closed");
function send() {
  const v = document.getElementById("msg").value;
  ws.send(v);
  log("> " + v);
}
</script>
`

func main() {
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", echo)
	mux.HandleFunc("/clock", clock)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(page))
	})

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		fmt.Println("listen:", err)
		os.Exit(1)
	}
	fmt.Println("listening on", addr, "backend="+httpserver.Backend)

	// The one line that is not ordinary net/http. Under standard Go it is
	// srv.Serve(ln); under TinyGo it routes the upgrade around net/http's
	// background read, which would otherwise deadlock Hijack.
	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	if err := httpserver.Serve(ln, srv); err != nil {
		fmt.Println("serve:", err)
		os.Exit(1)
	}
}
