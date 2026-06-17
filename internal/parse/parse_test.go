package parse

import (
	"os"
	"testing"
)

func TestParseRSS(t *testing.T) {
	data, _ := os.ReadFile("testdata/sample_rss.xml")
	pf, err := New().Parse(data, "https://sample.test/feed.xml")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if pf.Title != "Sample" || len(pf.Entries) != 1 {
		t.Fatalf("parsed = %+v", pf)
	}
	e := pf.Entries[0]
	if e.GUID != "post-1" {
		t.Fatalf("guid = %q", e.GUID)
	}
	if e.URL != "https://sample.test/posts/1" {
		t.Fatalf("relative url not resolved: %q", e.URL)
	}
	if e.Hash == "" {
		t.Fatal("hash must be set")
	}
}

func TestDiscover(t *testing.T) {
	data, _ := os.ReadFile("testdata/page_with_feed.html")
	urls, err := New().Discover(data, "https://abs.test/blog/")
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(urls) != 2 || urls[0] != "https://abs.test/feed.xml" || urls[1] != "https://abs.test/atom" {
		t.Fatalf("discovered = %v", urls)
	}
}
