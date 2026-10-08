package ratelimit

import (
	"net/http/httptest"
	"testing"
)

func TestClientIPResolver_IgnoresForwardingHeadersFromUntrustedPeer(t *testing.T) {
	resolver, err := NewClientIPResolver([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatalf("NewClientIPResolver() error = %v", err)
	}
	request := httptest.NewRequest("GET", "/", nil)
	request.RemoteAddr = "203.0.113.9:4567"
	request.Header.Set("X-Forwarded-For", "198.51.100.20")

	if got := resolver.ClientIP(request); got != "203.0.113.9" {
		t.Fatalf("ClientIP() = %q, want direct peer", got)
	}
}

func TestClientIPResolver_UsesFirstUntrustedAddressFromRight(t *testing.T) {
	resolver, err := NewClientIPResolver([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatalf("NewClientIPResolver() error = %v", err)
	}
	request := httptest.NewRequest("GET", "/", nil)
	request.RemoteAddr = "10.0.0.3:4567"
	request.Header.Set("X-Forwarded-For", "198.51.100.20, 10.0.0.1, 10.0.0.2")

	if got := resolver.ClientIP(request); got != "198.51.100.20" {
		t.Fatalf("ClientIP() = %q, want forwarded client", got)
	}
}

func TestClientIPResolver_CanonicalizesIPv4MappedAddress(t *testing.T) {
	resolver, err := NewClientIPResolver([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatalf("NewClientIPResolver() error = %v", err)
	}
	request := httptest.NewRequest("GET", "/", nil)
	request.RemoteAddr = "10.0.0.3:4567"
	request.Header.Set("X-Forwarded-For", "::ffff:198.51.100.20")

	if got := resolver.ClientIP(request); got != "198.51.100.20" {
		t.Fatalf("ClientIP() = %q, want canonical IPv4", got)
	}
}

func TestClientIPResolver_UsesXRealIPFromTrustedPeer(t *testing.T) {
	resolver, err := NewClientIPResolver([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatalf("NewClientIPResolver() error = %v", err)
	}
	request := httptest.NewRequest("GET", "/", nil)
	request.RemoteAddr = "10.0.0.3:4567"
	request.Header.Set("X-Real-IP", "2001:db8::20")

	if got := resolver.ClientIP(request); got != "2001:db8::20" {
		t.Fatalf("ClientIP() = %q, want X-Real-IP", got)
	}
}
