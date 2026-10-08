package ratelimit

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

type ClientIPResolver struct {
	trustedProxies []netip.Prefix
}

func NewClientIPResolver(cidrs []string) (*ClientIPResolver, error) {
	prefixes := make([]netip.Prefix, 0, len(cidrs))
	for _, cidr := range cidrs {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(cidr))
		if err != nil {
			return nil, fmt.Errorf("parse trusted proxy CIDR %q: %w", cidr, err)
		}
		prefixes = append(prefixes, prefix.Masked())
	}
	return &ClientIPResolver{trustedProxies: prefixes}, nil
}

func (resolver *ClientIPResolver) ClientIP(r *http.Request) string {
	remote, ok := parseIP(r.RemoteAddr)
	if !ok {
		return strings.TrimSpace(r.RemoteAddr)
	}
	if !resolver.isTrusted(remote) {
		return remote.String()
	}

	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); forwarded != "" {
		parts := strings.Split(forwarded, ",")
		var leftmost netip.Addr
		for index := len(parts) - 1; index >= 0; index-- {
			candidate, valid := parseIP(parts[index])
			if !valid {
				return remote.String()
			}
			leftmost = candidate
			if !resolver.isTrusted(candidate) {
				return candidate.String()
			}
		}
		if leftmost.IsValid() {
			return leftmost.String()
		}
	}

	if realIP, valid := parseIP(r.Header.Get("X-Real-IP")); valid {
		return realIP.String()
	}
	return remote.String()
}

func (resolver *ClientIPResolver) isTrusted(address netip.Addr) bool {
	for _, prefix := range resolver.trustedProxies {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

func parseIP(value string) (netip.Addr, bool) {
	value = strings.TrimSpace(value)
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	value = strings.Trim(value, "[]")
	address, err := netip.ParseAddr(value)
	if err != nil {
		return netip.Addr{}, false
	}
	if address.Zone() != "" {
		address = address.WithZone("")
	}
	return address.Unmap(), true
}
