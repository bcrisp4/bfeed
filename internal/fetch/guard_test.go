package fetch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"github.com/bcrisp4/bfeed/internal/core"
)

func TestIsBlockedIP(t *testing.T) {
	blocked := []string{
		"127.0.0.1", "10.0.0.5", "192.168.1.1", "172.16.0.1",
		"169.254.169.254", "100.64.0.1", "::1", "fc00::1", "fe80::1", "0.0.0.0",
		"::",                   // IPv6 unspecified
		"224.0.0.1", "ff02::1", // multicast (v4 + v6 link-local)
		"::ffff:127.0.0.1", // IPv4-mapped loopback (Unmap path)
		"0.1.2.3",          // 0.0.0.0/8 "this network"
		"240.0.0.1",        // reserved Class E
		"2002:7f00:1::1",   // 6to4 encoding 127.0.0.1
		"64:ff9b::7f00:1",  // NAT64 encoding 127.0.0.1
	}
	for _, s := range blocked {
		if !isBlockedIP(netip.MustParseAddr(s)) {
			t.Errorf("%s should be blocked", s)
		}
	}
	allowed := []string{"8.8.8.8", "1.1.1.1", "93.184.216.34", "2606:2800:220:1:248:1893:25c8:1946"}
	for _, s := range allowed {
		if isBlockedIP(netip.MustParseAddr(s)) {
			t.Errorf("%s should be allowed", s)
		}
	}
}

func TestFetchBlocksPrivateByDefault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("img"))
	}))
	defer srv.Close()
	c := New(Config{BlockPrivateNetworks: true, Timeout: 5 * time.Second})
	if _, err := c.Fetch(context.Background(), core.FetchRequest{URL: srv.URL}); err == nil {
		t.Fatal("expected SSRF block for loopback address, got nil")
	}
}

func TestFetchAllowsPrivateWhenAllowlisted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("img"))
	}))
	defer srv.Close()
	c := New(Config{
		BlockPrivateNetworks: true,
		AllowedCIDRs:         []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")},
		Timeout:              5 * time.Second,
	})
	resp, err := c.Fetch(context.Background(), core.FetchRequest{URL: srv.URL})
	if err != nil {
		t.Fatalf("allowlisted fetch failed: %v", err)
	}
	if resp.Status != 200 {
		t.Fatalf("status = %d", resp.Status)
	}
}
