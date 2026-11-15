package oauth

// @better-auth/core utils/host.ts: classifyHost and the loopback checks.

import (
	"fmt"
	"net/netip"
	"regexp"
	"strings"
)

var (
	metadataHosts = map[string]bool{"metadata.google.internal": true, "metadata.goog": true, "metadata": true, "instance-data": true, "instance-data.ec2.internal": true}
	ipv4Like      = regexp.MustCompile(`^\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}$`)
	trailingDots  = regexp.MustCompile(`\.+$`)
)

func stripPort(h string) string {
	if strings.HasPrefix(h, "[") {
		end := strings.Index(h, "]")
		if end < 0 {
			return h
		}
		return h[:end+1]
	}
	first := strings.Index(h, ":")
	if first < 0 || strings.Contains(h[first+1:], ":") {
		return h
	}
	return h[:first]
}

func stripBrackets(h string) string {
	if len(h) >= 2 && strings.HasPrefix(h, "[") && strings.HasSuffix(h, "]") {
		return h[1 : len(h)-1]
	}
	return h
}

func ipv4Uint(ip string) uint32 {
	var a, b, c, d uint32
	fmt.Sscanf(ip, "%d.%d.%d.%d", &a, &b, &c, &d)
	return a<<24 | b<<16 | c<<8 | d
}

func inV4(n uint32, prefix string, length uint) bool {
	if length == 0 {
		return true
	}
	mask := ^uint32(0) << (32 - length)
	return n&mask == ipv4Uint(prefix)&mask
}

func classifyIPv4(ip string) string {
	switch ip {
	case "0.0.0.0":
		return "unspecified"
	case "255.255.255.255":
		return "broadcast"
	}
	n := ipv4Uint(ip)
	for _, r := range []struct {
		p    string
		l    uint
		kind string
	}{
		{"127.0.0.0", 8, "loopback"}, {"10.0.0.0", 8, "private"}, {"172.16.0.0", 12, "private"}, {"192.168.0.0", 16, "private"},
		{"169.254.0.0", 16, "linkLocal"}, {"100.64.0.0", 10, "sharedAddressSpace"}, {"192.0.2.0", 24, "documentation"},
		{"198.51.100.0", 24, "documentation"}, {"203.0.113.0", 24, "documentation"}, {"198.18.0.0", 15, "benchmarking"},
		{"224.0.0.0", 4, "multicast"}, {"0.0.0.0", 8, "reserved"}, {"192.0.0.0", 24, "reserved"}, {"192.88.99.0", 24, "reserved"},
		{"240.0.0.0", 4, "reserved"},
	} {
		if inV4(n, r.p, r.l) {
			return r.kind
		}
	}
	return "public"
}

func expandV6(a netip.Addr) string {
	b := a.As16()
	parts := make([]string, 8)
	for i := 0; i < 8; i++ {
		parts[i] = fmt.Sprintf("%02x%02x", b[2*i], b[2*i+1])
	}
	return strings.Join(parts, ":")
}

func embeddedV4(expanded string, group int, xor bool) string {
	var g1, g2 uint32
	off := group * 5
	if _, err := fmt.Sscanf(expanded[off:off+4], "%x", &g1); err != nil {
		return ""
	}
	if _, err := fmt.Sscanf(expanded[off+5:off+9], "%x", &g2); err != nil {
		return ""
	}
	c := g1<<16 | g2
	if xor {
		c ^= 0xffffffff
	}
	return fmt.Sprintf("%d.%d.%d.%d", c>>24&255, c>>16&255, c>>8&255, c&255)
}

func classifyIPv6(e string) string {
	switch e {
	case "0000:0000:0000:0000:0000:0000:0000:0000":
		return "unspecified"
	case "0000:0000:0000:0000:0000:0000:0000:0001":
		return "loopback"
	}
	var first, second uint32
	fmt.Sscanf(e[0:2], "%x", &first)
	fmt.Sscanf(e[2:4], "%x", &second)
	switch {
	case first == 255:
		return "multicast"
	case first == 254 && second&192 == 128:
		return "linkLocal"
	case first == 254 && second&192 == 192:
		return "reserved"
	case first&254 == 252:
		return "private"
	case strings.HasPrefix(e, "2001:0db8:"):
		return "documentation"
	case strings.HasPrefix(e, "2001:0002:0000:"):
		return "benchmarking"
	case strings.HasPrefix(e, "2002:"):
		if v := embeddedV4(e, 1, false); v != "" && classifyIPv4(v) != "public" {
			return "reserved"
		}
		return "public"
	case strings.HasPrefix(e, "0064:ff9b:0000:0000:0000:0000:"), strings.HasPrefix(e, "0064:ff9b:0001:"), strings.HasPrefix(e, "2001:0000:"), strings.HasPrefix(e, "0100:0000:0000:0000:"):
		return "reserved"
	case strings.HasPrefix(e, "3fff:0"):
		return "documentation"
	case strings.HasPrefix(e, "5f00:"), strings.HasPrefix(e, "0000:0000:0000:0000:0000:0000:"):
		return "reserved"
	}
	return "public"
}

// classifyHost is classifyHost(host).kind.
func classifyHost(host string) string {
	h := strings.TrimSpace(host)
	h = stripBrackets(stripPort(h))
	if i := strings.Index(h, "%"); i >= 0 {
		h = h[:i]
	}
	h = strings.ToLower(trailingDots.ReplaceAllString(h, ""))
	if h == "" {
		return "reserved"
	}
	if ipv4Like.MatchString(h) {
		if a, err := netip.ParseAddr(h); err == nil && a.Is4() {
			return classifyIPv4(h)
		}
	}
	a, err := netip.ParseAddr(h)
	if err != nil {
		if h == "localhost" || strings.HasSuffix(h, ".localhost") {
			return "localhost"
		}
		if metadataHosts[h] {
			return "cloudMetadata"
		}
		return "public"
	}
	if a.Is4In6() {
		return classifyIPv4(a.Unmap().String())
	}
	if a.Is4() {
		return classifyIPv4(a.String())
	}
	return classifyIPv6(expandV6(a))
}

func isLoopbackHost(h string) bool {
	k := classifyHost(h)
	return k == "loopback" || k == "localhost"
}

func isLoopbackIP(h string) bool { return classifyHost(h) == "loopback" }

func isPrivateHostname(h string) bool { return classifyHost(h) != "public" }
