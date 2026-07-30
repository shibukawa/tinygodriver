package netdev

import (
	"encoding/binary"
	"net/netip"
	"os"
	"strings"
	"time"
)

// lookupHost resolves a name to one IPv4 address.
//
// The order is deliberate:
//
//  1. localhost, which must never depend on a resolver being reachable
//  2. the hosts file, which is the documented escape hatch when the rest fails
//  3. NETDEV_DNS, because an explicit override has to beat everything below it
//  4. the system resolver, which knows the search list and any domain-scoped
//     resolvers a VPN installed
//  5. the built-in UDP query, for builds with no system resolver to call
//
// Step 4 is what makes an internal name resolvable. Step 5 alone cannot: it
// queries whatever nameserver it can discover, and on Windows there is nothing
// to discover, so it always ended up asking a public resolver about a private
// zone.
func lookupHost(name string) (netip.Addr, error) {
	name = strings.TrimSuffix(strings.ToLower(name), ".")
	if name == "localhost" {
		return netip.AddrFrom4([4]byte{127, 0, 0, 1}), nil
	}
	if ip, ok := lookupHostsFile(name); ok {
		return ip, nil
	}

	// An explicit override means the caller knows which resolver can answer,
	// so consulting the system one first would defeat the point.
	if ns := nameserversFromEnv(); len(ns) > 0 {
		return queryNameservers(name, ns)
	}

	if ip, ok := systemLookupHost(name); ok {
		return ip, nil
	}
	return queryNameservers(name, nameservers())
}

func queryNameservers(name string, list []netip.AddrPort) (netip.Addr, error) {
	for _, ns := range list {
		ip, err := dnsQueryA(name, ns)
		if err == nil {
			return ip, nil
		}
	}
	return netip.Addr{}, ErrHostUnknown
}

func lookupHostsFile(name string) (netip.Addr, bool) {
	data, err := os.ReadFile(hostsPath())
	if err != nil {
		return netip.Addr{}, false
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if i := strings.Index(line, "#"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		ip, err := netip.ParseAddr(fields[0])
		if err != nil || !ip.Is4() {
			continue
		}
		for _, host := range fields[1:] {
			if strings.ToLower(host) == name {
				return ip, true
			}
		}
	}
	return netip.Addr{}, false
}

// NetdevDNSEnv names the environment variable that overrides which resolvers
// are used. The value is a comma or space separated list of IPv4 addresses,
// each with an optional :port that defaults to 53:
//
//	NETDEV_DNS=10.0.0.53
//	NETDEV_DNS=10.0.0.53,10.0.0.54:5353
//
// It exists because the configuration this package can discover on its own is
// not always the configuration the machine actually uses. On windows there is
// no /etc/resolv.conf at all, so without this every lookup went to 8.8.8.8 and
// no internal name could ever resolve. On macOS /etc/resolv.conf is a legacy
// file that the OS itself documents as not consulted, so it misses the
// domain-scoped resolvers a VPN installs.
const NetdevDNSEnv = "NETDEV_DNS"

// nameserversFromEnv parses NETDEV_DNS. An unparseable entry is skipped rather
// than failing the lookup, so one typo cannot take out a whole list.
func nameserversFromEnv() []netip.AddrPort {
	raw := os.Getenv(NetdevDNSEnv)
	if raw == "" {
		return nil
	}
	var out []netip.AddrPort
	for _, field := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t'
	}) {
		if ap, err := netip.ParseAddrPort(field); err == nil {
			if ap.Addr().Is4() {
				out = append(out, ap)
			}
			continue
		}
		if ip, err := netip.ParseAddr(field); err == nil && ip.Is4() {
			out = append(out, netip.AddrPortFrom(ip, 53))
		}
	}
	return out
}

func nameservers() []netip.AddrPort {
	// The override wins outright. Falling back to the discovered list would
	// hide a typo behind a resolver that cannot answer internal names.
	if out := nameserversFromEnv(); len(out) > 0 {
		return out
	}
	var out []netip.AddrPort
	data, err := os.ReadFile(resolvPath())
	if err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "nameserver") {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			ip, err := netip.ParseAddr(fields[1])
			if err != nil || !ip.Is4() {
				continue
			}
			out = append(out, netip.AddrPortFrom(ip, 53))
		}
	}
	if len(out) == 0 {
		out = append(out, netip.AddrPortFrom(netip.AddrFrom4([4]byte{8, 8, 8, 8}), 53))
	}
	return out
}

func dnsQueryA(name string, ns netip.AddrPort) (netip.Addr, error) {
	fd, err := sysSocket(AF_INET, SOCK_DGRAM, IPPROTO_UDP)
	if err != nil {
		return netip.Addr{}, err
	}
	defer sysClose(fd)

	msg := buildDNSQuery(name)
	if err := sysConnect(fd, ns); err != nil {
		return netip.Addr{}, err
	}
	deadline := time.Now().Add(3 * time.Second)
	if err := waitWrite(fd, deadline); err != nil {
		return netip.Addr{}, err
	}
	if _, err := sysSend(fd, msg, 0); err != nil {
		return netip.Addr{}, err
	}
	if err := waitRead(fd, deadline); err != nil {
		return netip.Addr{}, err
	}
	buf := make([]byte, 512)
	n, err := sysRecv(fd, buf, 0)
	if err != nil || n < 12 {
		return netip.Addr{}, ErrHostUnknown
	}
	return parseDNSAnswer(buf[:n])
}

func buildDNSQuery(name string) []byte {
	// Header: id=0x1234, recursion desired, 1 question.
	msg := []byte{
		0x12, 0x34,
		0x01, 0x00,
		0x00, 0x01,
		0x00, 0x00,
		0x00, 0x00,
		0x00, 0x00,
	}
	for _, label := range strings.Split(name, ".") {
		if label == "" {
			continue
		}
		msg = append(msg, byte(len(label)))
		msg = append(msg, label...)
	}
	msg = append(msg, 0x00)
	// QTYPE A, QCLASS IN
	msg = append(msg, 0x00, 0x01, 0x00, 0x01)
	return msg
}

func parseDNSAnswer(msg []byte) (netip.Addr, error) {
	if len(msg) < 12 {
		return netip.Addr{}, ErrHostUnknown
	}
	ancount := int(binary.BigEndian.Uint16(msg[6:8]))
	if ancount == 0 {
		return netip.Addr{}, ErrHostUnknown
	}
	// Skip header + question
	i := 12
	for i < len(msg) && msg[i] != 0 {
		if msg[i]&0xC0 == 0xC0 {
			i += 2
			break
		}
		i += int(msg[i]) + 1
	}
	if i < len(msg) && msg[i] == 0 {
		i++
	}
	i += 4 // qtype + qclass
	for a := 0; a < ancount && i+12 <= len(msg); a++ {
		if msg[i]&0xC0 == 0xC0 {
			i += 2
		} else {
			for i < len(msg) && msg[i] != 0 {
				i += int(msg[i]) + 1
			}
			i++
		}
		if i+10 > len(msg) {
			break
		}
		typ := binary.BigEndian.Uint16(msg[i : i+2])
		i += 2
		i += 2 // class
		i += 4 // ttl
		rdlen := int(binary.BigEndian.Uint16(msg[i : i+2]))
		i += 2
		if i+rdlen > len(msg) {
			break
		}
		if typ == 1 && rdlen == 4 { // A
			var a4 [4]byte
			copy(a4[:], msg[i:i+4])
			return netip.AddrFrom4(a4), nil
		}
		i += rdlen
	}
	return netip.Addr{}, ErrHostUnknown
}
