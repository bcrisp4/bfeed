package parse

import (
	"os"
	"strings"
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

// B13: text-only title/author/feed fields must be reduced to plain text — an
// Atom type="html" title or double-encoded entities otherwise render as literal
// visible markup once the templates escape them.
func TestParseStripsMarkupFromTextFields(t *testing.T) {
	body := []byte(`<?xml version="1.0" encoding="utf-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title type="html">Ben&amp;#39;s &lt;em&gt;blog&lt;/em&gt;</title>
  <subtitle>News &amp;amp; &lt;b&gt;views&lt;/b&gt;</subtitle>
  <entry>
    <id>e1</id>
    <title type="html">Ben&amp;#39;s &lt;em&gt;post&lt;/em&gt;</title>
    <author><name>Jane &amp;lt;jane&amp;gt;</name></author>
  </entry>
</feed>`)
	pf, err := New().Parse(body, "application/atom+xml", "https://e.test/feed.xml")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if pf.Title != "Ben's blog" {
		t.Fatalf("feed title = %q, want %q", pf.Title, "Ben's blog")
	}
	if pf.Description != "News & views" {
		t.Fatalf("feed description = %q, want %q", pf.Description, "News & views")
	}
	if len(pf.Entries) != 1 {
		t.Fatalf("got %d entries", len(pf.Entries))
	}
	e := pf.Entries[0]
	if e.Title != "Ben's post" {
		t.Fatalf("entry title = %q, want %q", e.Title, "Ben's post")
	}
	if e.Author != "Jane <jane>" {
		t.Fatalf("entry author = %q, want %q", e.Author, "Jane <jane>")
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

// #99: a valid-UTF-8 feed with no encoding declaration anywhere (no XML decl
// encoding attr, no HTTP charset) whose first 1KB is pure ASCII must stay
// UTF-8. The charset sniff examines only the first 1024 bytes and its UTF-8
// autodetect needs a high byte in that window, so it would otherwise fall back
// to windows-1252 and mojibake every multibyte rune past the window
// ("’" -> "â€™") — the feed-path twin of the #96 scrape bug.
func TestParseUndeclaredValidUTF8StaysUTF8(t *testing.T) {
	pad := strings.Repeat("all ascii filler text pushing the interesting bytes past the sniff window. ", 20)
	body := []byte(`<rss version="2.0"><channel><title>t</title><item><title>i</title><description>` +
		pad + `It’s past the window — with multibyte punctuation.` +
		`</description></item></channel></rss>`)
	if idx := strings.IndexFunc(string(body), func(r rune) bool { return r > 0x7f }); idx < 1024 {
		t.Fatalf("fixture broken: first multibyte rune at byte %d, need >= 1024", idx)
	}
	pf, err := New().Parse(body, "application/rss+xml", "https://e.com/f")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(pf.Entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(pf.Entries))
	}
	sum := pf.Entries[0].Summary
	if !strings.Contains(sum, "It’s past the window — with multibyte punctuation.") {
		t.Fatalf("multibyte text mojibaked; summary tail = %q", sum[len(sum)-80:])
	}
}

// #99 guard: genuine cp1252 bytes (0x80–0x9F punctuation) are invalid UTF-8, so
// the utf8.Valid override must NOT fire for them — an undeclared real cp1252
// feed still gets transcoded by the windows-1252 fallback. 0x92 = cp1252 "’".
func TestParseUndeclaredCP1252StillTranscoded(t *testing.T) {
	body := []byte(`<rss version="2.0"><channel><title>It` + "\x92" +
		`s</title><item><title>i</title></item></channel></rss>`)
	pf, err := New().Parse(body, "application/rss+xml", "https://e.com/f")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if pf.Title != "It’s" {
		t.Fatalf("title = %q, want %q (cp1252 salvage lost?)", pf.Title, "It’s")
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

// B8/F4: JSON Feed 1.1 advertises autodiscovery with type="application/feed+json"
// (application/json was the 1.0-era value). Discover must accept it.
func TestDiscoverFeedJSON(t *testing.T) {
	html := `<html><head>` +
		`<link rel="alternate" type="application/feed+json" href="/feed.json">` +
		`</head></html>`
	urls, err := New().Discover([]byte(html), "https://abs.test/blog/")
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(urls) != 1 || urls[0] != "https://abs.test/feed.json" {
		t.Fatalf("discovered = %v, want [https://abs.test/feed.json]", urls)
	}
}

// B8/F3: GUID-less items with NO link and NO title (no identity at all) must
// not all collapse onto one row via a shared derived GUID — the item index is a
// last-resort disambiguator for this pathological case.
func TestParseGUIDlessEmptyItemsAreDistinct(t *testing.T) {
	// Two items, no <guid>, no <link>, no <title>.
	rss := `<?xml version="1.0"?><rss version="2.0"><channel><title>t</title>` +
		`<item><description>a</description></item>` +
		`<item><description>b</description></item>` +
		`</channel></rss>`
	pf, err := New().Parse([]byte(rss), "", "https://e.com/f")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(pf.Entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(pf.Entries))
	}
	if pf.Entries[0].GUID == pf.Entries[1].GUID {
		t.Fatalf("empty GUID-less items collided on GUID %q", pf.Entries[0].GUID)
	}
}

// B8/F3: a GUID-less item's derived GUID is length-prefixed, so link and title
// can't be juggled across the join delimiter to forge a collision between two
// otherwise-distinct items.
func TestParseGUIDlessDerivationNotInjectable(t *testing.T) {
	// item A: link="https://e.com/a|b", title="c"
	// item B: link="https://e.com/a",   title="b|c"
	// A bare "link|title" join would make these equal; length-prefixing must not.
	rss := `<?xml version="1.0"?><rss version="2.0"><channel><title>t</title>` +
		`<item><title>c</title><link>https://e.com/a|b</link></item>` +
		`<item><title>b|c</title><link>https://e.com/a</link></item>` +
		`</channel></rss>`
	pf, err := New().Parse([]byte(rss), "", "https://e.com/f")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(pf.Entries) != 2 || pf.Entries[0].GUID == pf.Entries[1].GUID {
		t.Fatalf("injectable GUID derivation: entries=%+v", pf.Entries)
	}
}

// B8/F3: EntryHash must not be delimiter-injectable — distinct (title,content,
// summary) triples that concatenate to the same "|"-joined string must hash
// differently, and identical inputs must hash the same (idempotent).
func TestEntryHashDisambiguatesAndIsStable(t *testing.T) {
	// Classic ambiguity under a bare "|" join: "a"+"|"+"b|c" == "a|b"+"|"+"c".
	h1 := EntryHash("a", "b|c", "")
	h2 := EntryHash("a|b", "c", "")
	if h1 == h2 {
		t.Fatalf("ambiguous triples hashed equal: %q", h1)
	}
	if a, b := EntryHash("x", "y", "z"), EntryHash("x", "y", "z"); a != b {
		t.Fatalf("EntryHash not stable for identical inputs: %q vs %q", a, b)
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
