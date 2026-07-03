package core

import "testing"

// B8/F2: normalizeFeedURL applies only the safe, non-lossy canonicalisations
// (lowercase scheme+host, strip default port, drop fragment). It must never
// merge genuinely-distinct feeds (http vs https, trailing slash, query params).
func TestNormalizeFeedURL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://Example.COM/feed.xml", "https://example.com/feed.xml"},
		{"HTTP://Example.com/Feed.XML", "http://example.com/Feed.XML"}, // path case preserved
		{"https://example.com:443/feed.xml", "https://example.com/feed.xml"},
		{"http://example.com:80/feed.xml", "http://example.com/feed.xml"},
		{"https://example.com/feed.xml#section", "https://example.com/feed.xml"},
		{"  https://example.com/feed.xml  ", "https://example.com/feed.xml"}, // trimmed
		// Non-lossy: these must be left DISTINCT.
		{"https://example.com:8443/feed.xml", "https://example.com:8443/feed.xml"}, // non-default port kept
		{"https://example.com/feed.xml?utm_source=rss", "https://example.com/feed.xml?utm_source=rss"},
		{"https://example.com/feed/", "https://example.com/feed/"}, // trailing slash kept
		{"  http://[::1  ", "http://[::1"},                         // unparseable → trimmed, unchanged
	}
	for _, tc := range cases {
		if got := normalizeFeedURL(tc.in); got != tc.want {
			t.Errorf("normalizeFeedURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	// Idempotent: normalizing a normal URL is a no-op.
	n := normalizeFeedURL("https://example.com/feed.xml")
	if normalizeFeedURL(n) != n {
		t.Errorf("normalizeFeedURL not idempotent: %q", n)
	}
}
