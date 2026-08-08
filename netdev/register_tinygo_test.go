//go:build tinygo

package netdev

// register_tinygo.go binds useNetdev to net.useNetdev with go:linkname, which
// only resolves if the net package is actually in the binary. A program using
// this driver imports net or net/http and so supplies it; the driver's own
// sources never do, because they need net/netip and nothing more. That leaves a
// TinyGo test binary with a dangling linkname and a link failure naming
// _net.useNetdev.
//
// This import is what supplies it. It sat unnoticed behind tls_test.go, which
// imported net for its own reasons and whose build constraint used to admit
// TinyGo on darwin -- so the suite failed to compile before it could fail to
// link, and fixing the constraint exposed this.
import _ "net"
