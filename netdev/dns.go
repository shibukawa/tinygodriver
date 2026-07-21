package netdev

import (
	"encoding/binary"
	"net/netip"
	"os"
	"strings"
	"time"
)

func lookupHost(name string) (netip.Addr, error) {
	name = strings.TrimSuffix(strings.ToLower(name), ".")
	if name == "localhost" {
		return netip.AddrFrom4([4]byte{127, 0, 0, 1}), nil
	}
	if ip, ok := lookupHostsFile(name); ok {
		return ip, nil
	}
	for _, ns := range nameservers() {
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

func nameservers() []netip.AddrPort {
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
