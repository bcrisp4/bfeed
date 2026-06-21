// Package imgproxy provides a signed, SSRF-safe image proxy: the Signer mints
// and verifies HMAC-signed /img URLs, and Handler serves them.
package imgproxy

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
)

// Signer signs and verifies image URLs with a keyed HMAC so the proxy only ever
// serves URLs bfeed itself produced (never an open relay).
type Signer struct{ secret []byte }

func NewSigner(secret []byte) *Signer { return &Signer{secret: secret} }

func (s *Signer) Sign(rawURL string) string {
	m := hmac.New(sha256.New, s.secret)
	_, _ = m.Write([]byte(rawURL))
	return hex.EncodeToString(m.Sum(nil))
}

func (s *Signer) Verify(rawURL, sig string) bool {
	want, err := hex.DecodeString(sig)
	if err != nil {
		return false
	}
	m := hmac.New(sha256.New, s.secret)
	_, _ = m.Write([]byte(rawURL))
	return hmac.Equal(want, m.Sum(nil))
}

// ProxyURL returns the same-origin proxy URL for rawURL. Relative on purpose —
// the browser resolves it against the page origin.
func (s *Signer) ProxyURL(rawURL string) string {
	return "/img?u=" + url.QueryEscape(rawURL) + "&s=" + s.Sign(rawURL)
}
