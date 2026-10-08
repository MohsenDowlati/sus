package urlguard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strings"
)

var (
	ErrInvalidURL        = errors.New("invalid target URL")
	ErrUnsupportedScheme = errors.New("target URL scheme must be http or https")
	ErrMissingHostname   = errors.New("target URL hostname is required")
	ErrResolutionFailed  = errors.New("target hostname could not be resolved")
	ErrBlockedAddress    = errors.New("target resolves to a blocked IP address")
	ErrSelfRedirect      = errors.New("target points to the shortener domain")
)

var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("fc00::/7"),
}

type Resolver interface {
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}

type Validator struct {
	resolver   Resolver
	ownDomains []string
}

func NewDefault(ownDomains []string) *Validator {
	return New(net.DefaultResolver, ownDomains)
}

func New(resolver Resolver, ownDomains []string) *Validator {
	normalizedDomains := make([]string, 0, len(ownDomains))
	for _, domain := range ownDomains {
		if normalized := normalizeHostname(domain); normalized != "" {
			normalizedDomains = append(normalizedDomains, normalized)
		}
	}
	return &Validator{resolver: resolver, ownDomains: normalizedDomains}
}

func (validator *Validator) Validate(ctx context.Context, rawURL string) (string, error) {
	parsed, err := url.ParseRequestURI(strings.TrimSpace(rawURL))
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidURL, err)
	}

	parsed.Scheme = strings.ToLower(parsed.Scheme)
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", ErrUnsupportedScheme
	}
	hostname := normalizeHostname(parsed.Hostname())
	if hostname == "" {
		return "", ErrMissingHostname
	}
	if hostname == "localhost" || strings.HasSuffix(hostname, ".localhost") {
		return "", fmt.Errorf("%w: %s", ErrBlockedAddress, hostname)
	}
	if validator.isOwnDomain(hostname) {
		return "", ErrSelfRedirect
	}

	if address, err := netip.ParseAddr(hostname); err == nil {
		if isBlockedAddress(address) {
			return "", fmt.Errorf("%w: %s", ErrBlockedAddress, address)
		}
		return parsed.String(), nil
	}

	addresses, err := validator.resolver.LookupNetIP(ctx, "ip", hostname)
	if err != nil || len(addresses) == 0 {
		return "", fmt.Errorf("%w: %s", ErrResolutionFailed, hostname)
	}
	for _, address := range addresses {
		if isBlockedAddress(address) {
			return "", fmt.Errorf("%w: %s resolves to %s", ErrBlockedAddress, hostname, address)
		}
	}

	return parsed.String(), nil
}

func (validator *Validator) isOwnDomain(hostname string) bool {
	for _, ownDomain := range validator.ownDomains {
		if hostname == ownDomain || strings.HasSuffix(hostname, "."+ownDomain) {
			return true
		}
	}
	return false
}

func isBlockedAddress(address netip.Addr) bool {
	if address.Zone() != "" {
		address = address.WithZone("")
	}
	address = address.Unmap()
	for _, prefix := range blockedPrefixes {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

func normalizeHostname(hostname string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(hostname)), ".")
}
