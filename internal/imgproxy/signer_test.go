package imgproxy_test

import (
	"strings"
	"testing"

	"github.com/bcrisp4/bfeed/internal/imgproxy"
)

func TestSignVerifyRoundtrip(t *testing.T) {
	s := imgproxy.NewSigner([]byte("secret"))
	u := "https://ex.com/a.jpg"
	if !s.Verify(u, s.Sign(u)) {
		t.Fatal("roundtrip verify failed")
	}
}

func TestVerifyRejectsTamper(t *testing.T) {
	s := imgproxy.NewSigner([]byte("secret"))
	u := "https://ex.com/a.jpg"
	sig := s.Sign(u)
	if s.Verify(u+"x", sig) {
		t.Fatal("tampered url accepted")
	}
	if s.Verify(u, sig[:len(sig)-2]+"00") {
		t.Fatal("tampered sig accepted")
	}
	if s.Verify(u, "zzzz") {
		t.Fatal("non-hex sig accepted")
	}
}

func TestProxyURLContainsSignedURL(t *testing.T) {
	s := imgproxy.NewSigner([]byte("k"))
	got := s.ProxyURL("https://ex.com/a.jpg?x=1")
	if !strings.HasPrefix(got, "/img?u=") || !strings.Contains(got, "&s=") {
		t.Fatalf("unexpected proxy url: %s", got)
	}
}
