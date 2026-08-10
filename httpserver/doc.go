// Package httpserver serves net/http handlers on TinyGo when one of them needs
// to take over the connection.
//
// TinyGo's net/http Server cannot complete a protocol upgrade. Before calling a
// handler it starts a background read on the connection, and it cancels that
// read by moving the read deadline into the past. The netdev driver takes a
// deadline by value when a read begins, so it cannot interrupt a recv() already
// in flight: the cancellation never lands, and Hijack blocks forever. A
// WebSocket handshake hangs with no error, no panic and no log line.
//
// This package reads the request head itself and decides where the connection
// goes. Anything that is not an upgrade is handed to a real http.Server, with
// the head replayed, so keep-alive, timeouts and graceful shutdown keep working.
// An upgrade reaches the handler through a ResponseWriter that implements
// http.Hijacker, without net/http's background read in the way.
//
// One listener, one port. A WebSocket endpoint is one route among many:
//
//	mux := http.NewServeMux()
//	mux.HandleFunc("/healthz", healthz)
//	mux.HandleFunc("/ws", serveWebSocket) // calls Upgrader.Upgrade
//
//	ln, err := net.Listen("tcp", ":8080")
//	if err != nil {
//		return err
//	}
//	httpserver.Serve(ln, &http.Server{Handler: mux})
//
// Under standard Go, Serve calls srv.Serve(ln) and nothing else: net/http can
// hijack there, so none of this is needed. The same source builds and behaves
// the same way under both compilers.
//
// # When this package stops being necessary
//
// The whole package works around one upstream defect. It becomes unnecessary
// the moment TinyGo's net makes deadlines live, by re-checking a mutable
// deadline or polling a non-blocking socket, or TinyGo's net/http stops
// starting the background read. Either fix makes plain http.Server work, and
// callers can drop the Serve call for srv.Serve.
//
// # Known limits
//
// Only the first request on a connection is inspected. A browser opens a fresh
// connection for a WebSocket handshake, so this holds in practice; an upgrade
// arriving as a later request on a reused connection is answered 501 rather
// than deadlocking. Inspecting every request would mean reimplementing
// http.Server, which this package deliberately does not do.
//
// The bypass ResponseWriter exists to be hijacked. It implements Header, Write,
// WriteHeader and Hijack. It does not implement Flush, ReadFrom, CloseNotify,
// trailers or chunked encoding: a handler that needs those is not an upgrade
// handler and should not be reached through the bypass predicate.
package httpserver
