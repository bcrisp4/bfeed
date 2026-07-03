package parse

import (
	"os"
	"testing"
	"time"
)

func TestParseRSS(t *testing.T) {
	data, _ := os.ReadFile("testdata/sample_rss.xml")
	pf, err := New().Parse(data, "", "https://sample.test/feed.xml")
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

// B4/F2: a non-UTF-8 feed whose charset lives only in the HTTP Content-Type
// (no XML encoding declaration) must be transcoded, not hard-fail "invalid UTF-8".
// windows-1251 bytes: 0xd2 0xe5 0xf1 0xf2 = "Тест".
func TestParseHonorsHTTPCharsetWhenNoEncodingDecl(t *testing.T) {
	body := []byte(`<?xml version="1.0"?><rss version="2.0"><channel><title>` +
		"\xd2\xe5\xf1\xf2" + `</title><item><title>i</title></item></channel></rss>`)
	pf, err := New().Parse(body, "application/rss+xml; charset=windows-1251", "https://e.com/f")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if pf.Title != "Тест" {
		t.Fatalf("title = %q, want decoded %q", pf.Title, "Тест")
	}
}

// B4/F2 guard: an in-band encoding declaration plus a matching HTTP charset must
// still decode correctly — not double-transcode into mojibake. "no space" is the
// spelling gofeed honors (so our decoder leaves the raw bytes for gofeed to
// transcode); "space around eq" is a spelling gofeed does NOT honor (verified: it
// fails "invalid UTF-8" raw), so our decoder must pre-transcode it instead. Both
// paths must yield the same correct text.
func TestParseNoDoubleTranscodeWhenEncodingDeclared(t *testing.T) {
	title := "\xd2\xe5\xf1\xf2" // windows-1251 "Тест"
	cases := []struct{ name, decl string }{
		{"no space", `<?xml version="1.0" encoding="windows-1251"?>`},
		{"space around eq", `<?xml version="1.0" encoding = "windows-1251"?>`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(tc.decl + `<rss version="2.0"><channel><title>` +
				title + `</title><item><title>i</title></item></channel></rss>`)
			pf, err := New().Parse(body, "application/rss+xml; charset=windows-1251", "https://e.com/f")
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if pf.Title != "Тест" {
				t.Fatalf("title = %q, want %q (double-transcode corruption?)", pf.Title, "Тест")
			}
		})
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

func TestParseTTL(t *testing.T) {
	rssTTL := `<?xml version="1.0"?><rss version="2.0"><channel><title>t</title>
		<ttl>45</ttl><item><title>i</title></item></channel></rss>`
	rssSy := `<?xml version="1.0"?><rss version="2.0"
		xmlns:sy="http://purl.org/rss/1.0/modules/syndication/"><channel><title>t</title>
		<sy:updatePeriod>hourly</sy:updatePeriod><sy:updateFrequency>2</sy:updateFrequency>
		<item><title>i</title></item></channel></rss>`
	rssBoth := `<?xml version="1.0"?><rss version="2.0"
		xmlns:sy="http://purl.org/rss/1.0/modules/syndication/"><channel><title>t</title>
		<ttl>45</ttl><sy:updatePeriod>daily</sy:updatePeriod>
		<item><title>i</title></item></channel></rss>`
	atom := `<?xml version="1.0"?><feed xmlns="http://www.w3.org/2005/Atom">
		<title>t</title><entry><title>i</title></entry></feed>`

	cases := []struct {
		name string
		xml  string
		want time.Duration
	}{
		{"rss ttl minutes", rssTTL, 45 * time.Minute},
		{"sy hourly /2", rssSy, 30 * time.Minute},
		{"both -> max", rssBoth, 24 * time.Hour}, // daily(24h) > ttl 45m
		{"atom none", atom, 0},
	}
	p := New()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pf, err := p.Parse([]byte(tc.xml), "", "https://e.com/f")
			if err != nil {
				t.Fatal(err)
			}
			if pf.TTL != tc.want {
				t.Fatalf("TTL = %v, want %v", pf.TTL, tc.want)
			}
		})
	}
}

func TestParseTTLChannelScoped(t *testing.T) {
	cases := []struct {
		name string
		xml  string
		want time.Duration
	}{
		{"namespaced ttl ignored", `<?xml version="1.0"?><rss version="2.0" xmlns:media="http://x">` +
			`<channel><title>t</title><media:ttl>5</media:ttl><item><title>i</title></item></channel></rss>`, 0},
		{"item-level ttl ignored", `<?xml version="1.0"?><rss version="2.0"><channel><title>t</title>` +
			`<item><title>i</title><ttl>5</ttl></item></channel></rss>`, 0},
		{"channel ttl before items wins", `<?xml version="1.0"?><rss version="2.0"><channel><title>t</title>` +
			`<ttl>45</ttl><item><title>i</title><ttl>9</ttl></item></channel></rss>`, 45 * time.Minute},
	}
	p := New()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pf, err := p.Parse([]byte(tc.xml), "", "https://e.com/f")
			if err != nil {
				t.Fatal(err)
			}
			if pf.TTL != tc.want {
				t.Fatalf("TTL = %v, want %v", pf.TTL, tc.want)
			}
		})
	}
}
