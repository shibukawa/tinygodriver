package netdev

import (
	"encoding/binary"
	"net/netip"
	"os"
	"strings"
	"sync"
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
	// A resolver is asked about the same few names over and over: every dial
	// resolves its host again. Serving the answer from memory for its TTL
	// avoids a UDP round trip per connection; only positive answers are kept,
	// so a name that starts resolving is never held back by a cached failure.
	if ip, ok := answerCache.get(name); ok {
		return ip, nil
	}

	// Keep the last underlying failure. Collapsing every one into a bare
	// "Host unknown" hides the difference between a name that does not
	// resolve and a build that cannot open a socket at all, which is what a
	// WASI target reports. errors.Is still matches ErrHostUnknown.
	var lastErr error
	for _, ns := range list {
		ip, ttl, err := dnsQueryA(name, ns)
		if err == nil {
			answerCache.put(name, ip, ttl)
			return ip, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return netip.Addr{}, &wrappedError{sentinel: ErrHostUnknown, cause: lastErr}
	}
	return netip.Addr{}, ErrHostUnknown
}

// answerCache holds resolved names for their TTL.
//
// The store is a slice searched linearly, not a map: a process resolves a
// handful of names, and on TinyGo every distinct map type instantiates its own
// hashing and growth code, which is real binary size for no gain at this
// scale.
var answerCache = dnsCache{}

type dnsCache struct {
	mu      sync.Mutex
	entries []dnsCacheEntry
}

type dnsCacheEntry struct {
	name    string
	ip      netip.Addr
	expires time.Time
}

const (
	// dnsCacheMinTTL keeps a zero-TTL answer for a moment anyway, so a burst
	// of dials does not turn into a burst of identical queries.
	dnsCacheMinTTL = 5 * time.Second

	// dnsCacheMaxTTL bounds staleness whatever the record claims.
	dnsCacheMaxTTL = 5 * time.Minute

	// dnsCacheMaxEntries bounds the cache. When the bound is hit, starting
	// over is cheaper than tracking recency.
	dnsCacheMaxEntries = 64
)

func (c *dnsCache) get(name string) (netip.Addr, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.entries {
		entry := &c.entries[i]
		if entry.name != name {
			continue
		}
		if time.Now().After(entry.expires) {
			c.entries[i] = c.entries[len(c.entries)-1]
			c.entries = c.entries[:len(c.entries)-1]
			return netip.Addr{}, false
		}
		return entry.ip, true
	}
	return netip.Addr{}, false
}

func (c *dnsCache) put(name string, ip netip.Addr, ttl time.Duration) {
	if ttl < dnsCacheMinTTL {
		ttl = dnsCacheMinTTL
	}
	if ttl > dnsCacheMaxTTL {
		ttl = dnsCacheMaxTTL
	}
	entry := dnsCacheEntry{name: name, ip: ip, expires: time.Now().Add(ttl)}
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.entries {
		if c.entries[i].name == name {
			c.entries[i] = entry
			return
		}
	}
	if len(c.entries) >= dnsCacheMaxEntries {
		c.entries = c.entries[:0]
	}
	c.entries = append(c.entries, entry)
}

// wrappedError carries a sentinel and the underlying cause without pulling in
// the formatting machinery. errors.Is matches both.
type wrappedError struct {
	sentinel error
	cause    error
}

func (e *wrappedError) Error() string {
	return e.sentinel.Error() + ": " + e.cause.Error()
}

func (e *wrappedError) Unwrap() []error {
	return []error{e.sentinel, e.cause}
}

// fileIdentity is the stat identity a cached parse is keyed on. The mtime is
// kept as nanoseconds so revalidation compares integers instead of linking
// time.Time comparison.
type fileIdentity struct {
	size  int64
	mtime int64
}

// statIdentity reads a file's identity, reporting false when it cannot.
func statIdentity(path string) (fileIdentity, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return fileIdentity{}, false
	}
	return fileIdentity{size: info.Size(), mtime: info.ModTime().UnixNano()}, true
}

// hostsFileCache holds the parsed hosts file, revalidated by stat: a lookup
// costs one stat instead of a read and a full parse, and an edited file is
// still picked up on the next call.
//
// Entries are a slice, not a map, for the same binary-size reason as
// dnsCache; hosts files are small and scanned rarely.
var hostsFileCache struct {
	mu       sync.Mutex
	path     string
	identity fileIdentity
	entries  []hostsEntry
}

type hostsEntry struct {
	name string
	ip   netip.Addr
}

func lookupHostsFile(name string) (netip.Addr, bool) {
	return lookupHostsIn(hostsPath(), name)
}

func lookupHostsIn(path, name string) (netip.Addr, bool) {
	identity, ok := statIdentity(path)
	if !ok {
		return netip.Addr{}, false
	}

	c := &hostsFileCache
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.path != path || c.identity != identity {
		data, err := os.ReadFile(path)
		if err != nil {
			return netip.Addr{}, false
		}
		c.entries = parseHostsFile(data)
		c.path, c.identity = path, identity
	}
	for _, entry := range c.entries {
		if entry.name == name {
			return entry.ip, true
		}
	}
	return netip.Addr{}, false
}

func parseHostsFile(data []byte) []hostsEntry {
	var entries []hostsEntry
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
			entries = append(entries, hostsEntry{name: strings.ToLower(host), ip: ip})
		}
	}
	// Lookup scans in order, so the first entry for a name wins, as it did
	// when the file was scanned per lookup.
	return entries
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

// resolvConfCache holds the parsed resolv.conf, revalidated by stat like the
// hosts file.
var resolvConfCache struct {
	mu       sync.Mutex
	path     string
	identity fileIdentity
	list     []netip.AddrPort
}

func nameservers() []netip.AddrPort {
	// The override wins outright. Falling back to the discovered list would
	// hide a typo behind a resolver that cannot answer internal names.
	if out := nameserversFromEnv(); len(out) > 0 {
		return out
	}
	var out []netip.AddrPort
	path := resolvPath()
	if identity, ok := statIdentity(path); ok {
		c := &resolvConfCache
		c.mu.Lock()
		if c.path != path || c.identity != identity {
			if data, err := os.ReadFile(path); err == nil {
				c.list = parseResolvConf(data)
				c.path, c.identity = path, identity
			}
		}
		out = c.list
		c.mu.Unlock()
	}
	if len(out) == 0 {
		out = []netip.AddrPort{netip.AddrPortFrom(netip.AddrFrom4([4]byte{8, 8, 8, 8}), 53)}
	}
	return out
}

func parseResolvConf(data []byte) []netip.AddrPort {
	var out []netip.AddrPort
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
	return out
}

func dnsQueryA(name string, ns netip.AddrPort) (netip.Addr, time.Duration, error) {
	fd, err := sysSocket(AF_INET, SOCK_DGRAM, IPPROTO_UDP)
	if err != nil {
		return netip.Addr{}, 0, err
	}
	defer sysClose(fd)

	msg := buildDNSQuery(name)
	if err := sysConnect(fd, ns); err != nil {
		return netip.Addr{}, 0, err
	}
	deadline := time.Now().Add(3 * time.Second)
	if err := waitWrite(fd, deadline); err != nil {
		return netip.Addr{}, 0, err
	}
	if _, err := sysSend(fd, msg, 0); err != nil {
		return netip.Addr{}, 0, err
	}
	if err := waitRead(fd, deadline); err != nil {
		return netip.Addr{}, 0, err
	}
	buf := make([]byte, 512)
	n, err := sysRecv(fd, buf, 0)
	if err != nil || n < 12 {
		return netip.Addr{}, 0, ErrHostUnknown
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

func parseDNSAnswer(msg []byte) (netip.Addr, time.Duration, error) {
	if len(msg) < 12 {
		return netip.Addr{}, 0, ErrHostUnknown
	}
	ancount := int(binary.BigEndian.Uint16(msg[6:8]))
	if ancount == 0 {
		return netip.Addr{}, 0, ErrHostUnknown
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
		ttl := binary.BigEndian.Uint32(msg[i : i+4])
		i += 4
		rdlen := int(binary.BigEndian.Uint16(msg[i : i+2]))
		i += 2
		if i+rdlen > len(msg) {
			break
		}
		if typ == 1 && rdlen == 4 { // A
			var a4 [4]byte
			copy(a4[:], msg[i:i+4])
			return netip.AddrFrom4(a4), time.Duration(ttl) * time.Second, nil
		}
		i += rdlen
	}
	return netip.Addr{}, 0, ErrHostUnknown
}
