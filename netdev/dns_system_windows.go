//go:build windows && cgo

package netdev

/*
#cgo LDFLAGS: -lws2_32

#include <stdint.h>
#include <stdlib.h>
#include <string.h>

// Hand-declared for the same reason the rest of sys_windows.go is: TinyGo
// compiles cgo C without the system headers this file would otherwise need.
//
// Only the Windows layout is described here, and it is not the unix one:
// ADDRINFOA puts ai_canonname *before* ai_addr, and ai_addrlen is a size_t
// rather than a 32-bit socklen_t. Getting that backwards would read the
// address out of the wrong field, which is why the Wine test that exercises
// this is worth more than it looks.
struct netdev_addrinfo {
	int ai_flags;
	int ai_family;
	int ai_socktype;
	int ai_protocol;
	uint64_t ai_addrlen;
	char *ai_canonname;
	void *ai_addr;
	struct netdev_addrinfo *ai_next;
};

struct netdev_sockaddr_in {
	int16_t sin_family;
	uint16_t sin_port;
	uint8_t sin_addr[4];
	char sin_zero[8];
};

int getaddrinfo(const char *node, const char *service,
                const struct netdev_addrinfo *hints,
                struct netdev_addrinfo **res);
void freeaddrinfo(struct netdev_addrinfo *res);

#define NETDEV_AF_INET 2
#define NETDEV_SOCK_STREAM 1

// netdev_lookup4 resolves one name to the first IPv4 address Windows returns.
//
// Going through getaddrinfo rather than querying a nameserver directly is the
// whole point: the DNS Client service applies the suffix search list, the NRPT
// policy table and whatever a VPN has installed. A short internal name only
// resolves this way.
static int netdev_lookup4(const char *host, uint8_t *out) {
	struct netdev_addrinfo hints;
	memset(&hints, 0, sizeof(hints));
	hints.ai_family = NETDEV_AF_INET;
	hints.ai_socktype = NETDEV_SOCK_STREAM;

	struct netdev_addrinfo *res = NULL;
	if (getaddrinfo(host, NULL, &hints, &res) != 0 || res == NULL) {
		return -1;
	}
	for (struct netdev_addrinfo *p = res; p != NULL; p = p->ai_next) {
		if (p->ai_family == NETDEV_AF_INET && p->ai_addr != NULL) {
			struct netdev_sockaddr_in *sa = (struct netdev_sockaddr_in *)p->ai_addr;
			memcpy(out, sa->sin_addr, 4);
			freeaddrinfo(res);
			return 0;
		}
	}
	freeaddrinfo(res);
	return -1;
}
*/
import "C"

import (
	"net/netip"
	"unsafe"
)

// systemLookupHost asks Windows to resolve the name.
//
// This is what makes an internal host reachable at all. Without it netdev has
// no resolver configuration to find on Windows -- there is no
// /etc/resolv.conf -- so every lookup fell back to a public nameserver that
// cannot answer for a private zone.
//
// Reporting false rather than an error keeps the caller's fallback intact: a
// name this cannot resolve still goes to the UDP resolver afterwards.
func systemLookupHost(name string) (netip.Addr, bool) {
	if name == "" {
		return netip.Addr{}, false
	}
	if err := ensureWSA(); err != nil {
		return netip.Addr{}, false
	}

	host := C.CString(name)
	defer C.free(unsafe.Pointer(host))

	var out [4]byte
	if C.netdev_lookup4(host, (*C.uint8_t)(unsafe.Pointer(&out[0]))) != 0 {
		return netip.Addr{}, false
	}
	return netip.AddrFrom4(out), true
}
