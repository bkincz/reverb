package realip_test

import (
	"net/http/httptest"
	"testing"

	"github.com/bkincz/reverb/internal/realip"
)

func TestRemoteAddr(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "203.0.113.10:4567"

	if got := realip.RemoteAddr(req); got != "203.0.113.10" {
		t.Fatalf("RemoteAddr() = %q, want %q", got, "203.0.113.10")
	}
}

func TestResolver_ClientIPDirectRequestIgnoresForwardedHeaders(t *testing.T) {
	resolver, err := realip.New([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "203.0.113.10:4567"
	req.Header.Set("X-Forwarded-For", "198.51.100.3")

	if got := resolver.ClientIP(req); got != "203.0.113.10" {
		t.Fatalf("ClientIP() = %q, want %q", got, "203.0.113.10")
	}
}

func TestResolver_ClientIPUsesTrustedProxyChain(t *testing.T) {
	resolver, err := realip.New([]string{"10.0.0.0/8", "192.168.0.0/16"})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.2:4567"
	req.Header.Set("X-Forwarded-For", "198.51.100.3, 192.168.1.9")

	if got := resolver.ClientIP(req); got != "198.51.100.3" {
		t.Fatalf("ClientIP() = %q, want %q", got, "198.51.100.3")
	}
}

func TestResolver_ClientIPFallsBackToXRealIP(t *testing.T) {
	resolver, err := realip.New([]string{"10.0.0.2"})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.2:4567"
	req.Header.Set("X-Real-IP", "198.51.100.3")

	if got := resolver.ClientIP(req); got != "198.51.100.3" {
		t.Fatalf("ClientIP() = %q, want %q", got, "198.51.100.3")
	}
}

func TestNew_InvalidTrustedProxy(t *testing.T) {
	if _, err := realip.New([]string{"not-an-ip"}); err == nil {
		t.Fatal("expected invalid proxy entry to fail")
	}
}
