package urlguard

import (
	"context"
	"errors"
	"net/netip"
	"testing"
)

type fakeResolver struct {
	addresses map[string][]netip.Addr
	err       error
	calls     int
}

func (resolver *fakeResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	resolver.calls++
	if resolver.err != nil {
		return nil, resolver.err
	}
	return resolver.addresses[host], nil
}

func TestValidator_AllowsPublicHTTPAndHTTPS(t *testing.T) {
	resolver := &fakeResolver{addresses: map[string][]netip.Addr{
		"public.example": {netip.MustParseAddr("8.8.8.8")},
	}}
	validator := New(resolver, nil)

	for _, target := range []string{
		"http://public.example/path",
		"https://public.example/path?query=value",
		"https://1.1.1.1/resource",
	} {
		if _, err := validator.Validate(context.Background(), target); err != nil {
			t.Fatalf("Validate(%q) error = %v", target, err)
		}
	}
}

func TestValidator_RejectsUnsupportedSchemes(t *testing.T) {
	validator := New(&fakeResolver{}, nil)
	for _, target := range []string{
		"ftp://public.example/file",
		"file:///etc/passwd",
		"javascript:alert(1)",
	} {
		_, err := validator.Validate(context.Background(), target)
		if !errors.Is(err, ErrUnsupportedScheme) {
			t.Fatalf("Validate(%q) error = %v, want %v", target, err, ErrUnsupportedScheme)
		}
	}
}

func TestValidator_RejectsBlockedLiteralAddresses(t *testing.T) {
	validator := New(&fakeResolver{}, nil)
	targets := []string{
		"http://127.0.0.1",
		"http://127.255.255.255",
		"http://10.10.10.10",
		"http://172.16.0.1",
		"http://172.31.255.254",
		"http://192.168.1.1",
		"http://100.64.0.1",
		"http://100.127.255.254",
		"http://169.254.169.254/latest/meta-data",
		"http://[::1]",
		"http://[fe80::1]",
		"http://[fc00::1]",
		"http://[fd12:3456:789a::1]",
		"http://[::ffff:127.0.0.1]",
	}

	for _, target := range targets {
		_, err := validator.Validate(context.Background(), target)
		if !errors.Is(err, ErrBlockedAddress) {
			t.Fatalf("Validate(%q) error = %v, want %v", target, err, ErrBlockedAddress)
		}
	}
}

func TestValidator_RejectsLocalhostWithoutDNSLookup(t *testing.T) {
	resolver := &fakeResolver{addresses: map[string][]netip.Addr{
		"localhost": {netip.MustParseAddr("8.8.8.8")},
	}}
	validator := New(resolver, nil)

	for _, target := range []string{"http://localhost:8080", "http://service.localhost/path"} {
		_, err := validator.Validate(context.Background(), target)
		if !errors.Is(err, ErrBlockedAddress) {
			t.Fatalf("Validate(%q) error = %v, want %v", target, err, ErrBlockedAddress)
		}
	}
	if resolver.calls != 0 {
		t.Fatalf("DNS calls = %d, want 0 for localhost", resolver.calls)
	}
}

func TestValidator_RejectsHostnameWhenAnyResolvedAddressIsBlocked(t *testing.T) {
	resolver := &fakeResolver{addresses: map[string][]netip.Addr{
		"mixed.example": {
			netip.MustParseAddr("8.8.8.8"),
			netip.MustParseAddr("10.0.0.8"),
		},
	}}
	validator := New(resolver, nil)

	_, err := validator.Validate(context.Background(), "https://mixed.example/path")
	if !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("Validate() error = %v, want %v", err, ErrBlockedAddress)
	}
}

func TestValidator_RejectsSelfRedirectDomains(t *testing.T) {
	resolver := &fakeResolver{addresses: map[string][]netip.Addr{
		"sho.rt":     {netip.MustParseAddr("8.8.8.8")},
		"www.sho.rt": {netip.MustParseAddr("8.8.8.8")},
	}}
	validator := New(resolver, []string{"Sho.Rt."})

	for _, target := range []string{"https://sho.rt/abc", "https://SHO.RT./abc", "https://www.sho.rt/abc"} {
		_, err := validator.Validate(context.Background(), target)
		if !errors.Is(err, ErrSelfRedirect) {
			t.Fatalf("Validate(%q) error = %v, want %v", target, err, ErrSelfRedirect)
		}
	}
	if resolver.calls != 0 {
		t.Fatalf("DNS calls = %d, want 0 for self domains", resolver.calls)
	}
}

func TestValidator_DoesNotOvermatchSelfDomainSuffix(t *testing.T) {
	resolver := &fakeResolver{addresses: map[string][]netip.Addr{
		"sho.rt.evil.example": {netip.MustParseAddr("8.8.8.8")},
	}}
	validator := New(resolver, []string{"sho.rt"})

	if _, err := validator.Validate(context.Background(), "https://sho.rt.evil.example/path"); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidator_RejectsResolutionFailure(t *testing.T) {
	validator := New(&fakeResolver{err: errors.New("DNS unavailable")}, nil)
	_, err := validator.Validate(context.Background(), "https://unresolved.example")
	if !errors.Is(err, ErrResolutionFailed) {
		t.Fatalf("Validate() error = %v, want %v", err, ErrResolutionFailed)
	}
}
