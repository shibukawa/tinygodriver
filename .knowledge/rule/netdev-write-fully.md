---
id: rule:netdev-write-fully
type: rule
title: netdev Send Writes the Whole Buffer
---
`Device.Send` returns `len(buf)` or an error, never a short count with a nil error, because TinyGo's `net.TCPConn.Write` hands the driver's count straight to callers that trust io.Writer's contract.

```yaml
scope: system:tinygo-netdev, every stream socket path; TLS sessions already loop in C
measured: 2026-09-02, tinygo 0.42.0 darwin/arm64, scheduler threads
defect:
  what: >
    a blocking write(2) returns a partial count, not EINTR, when a signal lands
    after some bytes have moved. TinyGo's threads scheduler and its garbage
    collector stop the world with signals, so a 4 MiB write over loopback came
    back as 604596 bytes with err == nil, and 2168072 with the peer writing at
    the same time. tinygo-org/net's TCPConn.Write does one netdev.Send and
    returns whatever it got, on the dev branch as well as 0.42
  who_trusted_it: >
    gorilla/websocket's Conn.write checks err and ignores n, so the tail of a
    frame vanished, the peer parsed the next header from mid-payload, sent a
    close frame and closed. That was websocket.TestLargeMessage: EPIPE within
    ten milliseconds, "websocket: close sent", or an echo truncated at 2.2 MB.
    14 of 30 runs on the untouched tree, 13 of 30 after the RWMutex fix, so
    unrelated to it. bufio.Writer.Flush turns the same short write into
    io.ErrShortWrite instead, so fasthttp and net/http fail loudly rather than
    corrupt, but they fail
  why_the_driver: >
    the io.Writer contract belongs to net.Conn, which we do not own; the
    driver is the last layer we do. Looping there makes every consumer correct
    without patching each one
fix: >
  Device.Send loops: waitWrite for the deadline between attempts, sysSend the
  remainder, stop on error with the count so far. sysSend and sysRecv on darwin
  and linux also retry EINTR, matching accept and select; a read interrupted
  before any byte moved would otherwise surface as errno 4 to a caller that
  treats every read error as fatal
tls_paths_already_correct:
  securetransport: st_write loops until sent == want and reports a shortfall only with an error
  mbedtls: https_mbed_write accumulates written across mbedtls_ssl_write calls
  network_framework: nw_connection_send takes the whole buffer
verify: >
  netdev TestSendWritesWholeBuffer sends 4 MiB in one call over loopback and
  asserts both the returned count and what the peer received; run it under
  `tinygo test ./netdev`, where the short write reproduced on the first round
  before the loop. The scratch probe (20 rounds, half duplex) went from short
  writes on round 0 to none, websocket.TestLargeMessage from 13 of 30 failed
  to 0 of 30, and the full websocket suite ran 10 of 10 clean
related: rule:netdev-syscall-status for the errno discipline the retry follows
```
