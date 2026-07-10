package parse

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mmcdole/gofeed"
	ext "github.com/mmcdole/gofeed/extensions"
	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
	"golang.org/x/text/transform"

	"github.com/bcrisp4/bfeed/internal/core"
)

type Parser struct{ fp *gofeed.Parser }

func New() *Parser {
	fp := gofeed.NewParser()
	fp.RSSTranslator = &commentsTranslator{inner: &gofeed.DefaultRSSTranslator{}}
	// Eagerly assign the other translators too: gofeed lazily writes each
	// translator field on the first parse of that feed type, an unsynchronized
	// write to this Parser, which is shared across concurrent poller workers.
	fp.AtomTranslator = &gofeed.DefaultAtomTranslator{}
	fp.JSONTranslator = &gofeed.DefaultJSONTranslator{}
	return &Parser{fp: fp}
}

var _ core.FeedParser = (*Parser)(nil)

func (p *Parser) Parse(data []byte, contentType, feedURL string) (*core.ParsedFeed, error) {
	f, err := p.fp.Parse(decodeReader(data, contentType))
	if err != nil {
		return nil, fmt.Errorf("gofeed parse: %w", err)
	}
	base, _ := url.Parse(feedURL)
	// Feed title/description are text-only fields; strip any markup/entities here
	// (the parse adapter is the boundary that cleans external data) so recordSuccess
	// persists plain text — same rationale as the per-entry title normalization below.
	out := &core.ParsedFeed{Title: core.PlainText(f.Title), Description: core.PlainText(f.Description), SiteURL: resolve(base, f.Link)}
	out.TTL = feedTTL(f, data)
	for i, it := range f.Items {
		link := resolve(base, it.Link)
		guid := it.GUID
		if guid == "" {
			// Derive a stable key from link+title. Length-prefixed (not a bare
			// "|") so the two fields can't be juggled across the delimiter to
			// forge a collision (link="a|b",title="c" vs link="a",title="b|c").
			// link+title is kept as the whole key so a newest-first feed that
			// prepends items keeps each item's GUID stable across polls — an
			// index-based key would re-number every item on each new post and
			// duplicate the entire feed. The item index is a last-resort
			// disambiguator ONLY when both fields are empty (items with no
			// identity at all, which would otherwise all collapse to one row).
			parts := []string{link, it.Title}
			if link == "" && it.Title == "" {
				parts = append(parts, strconv.Itoa(i))
			}
			guid = hashStr(joinFields(parts...))
		}
		var pub time.Time
		if it.PublishedParsed != nil {
			pub = it.PublishedParsed.UTC()
		} else if it.UpdatedParsed != nil {
			pub = it.UpdatedParsed.UTC()
		}
		author := ""
		if it.Author != nil {
			author = core.PlainText(it.Author.Name)
		}
		// Title/author are text-only fields but gofeed returns them verbatim, so a
		// type="html" title or double-encoded entities carry markup/entities that
		// templates then escape into literal visible tags. Reduce to plain text
		// here. The GUID fallback above deliberately still hashes the RAW title:
		// changing an existing entry's identity key would resurrect/duplicate it.
		title := core.PlainText(it.Title)
		content := it.Content
		summary := it.Description
		out.Entries = append(out.Entries, core.ParsedEntry{
			GUID:        guid,
			URL:         link,
			Title:       title,
			Author:      author,
			Content:     content,
			Summary:     summary,
			CommentsURL: commentsURL(base, it.Custom[commentsCustomKey]),
			PublishedAt: pub,
			Hash:        EntryHash(title, content, summary),
		})
	}
	return out, nil
}

// decodeReader returns a UTF-8 reader for the feed bytes. gofeed only transcodes
// when the XML declaration names a non-UTF-8 encoding, so a feed whose charset
// lives only in the HTTP Content-Type (RFC 3023) fails as "invalid UTF-8". We
// bridge that gap with charset.DetermineEncoding, which honors the HTTP charset,
// a BOM, and content sniffing. Crucially we do this ONLY when the bytes carry no
// in-band encoding declaration: if we pre-transcoded a feed that also declares
// its encoding, encoding/xml would re-invoke gofeed's CharsetReader on the
// now-UTF-8 stream and double-transcode it into mojibake.
//
// Uncertain answers (no BOM, no header charset) lose to a full body of valid
// UTF-8: XML with no in-band declaration is UTF-8 by spec, and the sniff's
// guesses — it examines only the first 1024 bytes, so an ASCII-only prefix
// yields its windows-1252 fallback, and a stray `<meta charset=…>` token in
// feed content can name anything — would mojibake multibyte runes further
// down (#99, the feed-path twin of #96). Genuine legacy-encoded text fails
// utf8.Valid (cp1252 &co high bytes are bare UTF-8 continuation bytes), so
// the sniffed fallback still transcodes it. Deliberately broader than
// extract.decodeReader's windows-1252-only exception: HTML honors <meta>
// declarations, XML does not — don't "align" the two guards.
func decodeReader(data []byte, contentType string) io.Reader {
	if hasXMLEncodingDecl(data) {
		return bytes.NewReader(data)
	}
	enc, _, certain := charset.DetermineEncoding(data, contentType)
	if certain || !utf8.Valid(data) {
		return transform.NewReader(bytes.NewReader(data), enc.NewDecoder())
	}
	return bytes.NewReader(data)
}

// hasXMLEncodingDecl reports whether the bytes begin with an XML declaration that
// carries an encoding attribute (e.g. <?xml version="1.0" encoding="utf-8"?>). It
// scans only the prolog: past a leading BOM/whitespace, and no further than the
// closing "?>" of the declaration.
func hasXMLEncodingDecl(data []byte) bool {
	if len(data) > 512 {
		data = data[:512] // the declaration, if any, is in the prolog
	}
	s := strings.TrimLeft(string(data), "\ufeff \t\r\n") // strip BOM + leading whitespace
	if !strings.HasPrefix(s, "<?xml") {
		return false
	}
	end := strings.Index(s, "?>")
	if end < 0 {
		return false
	}
	// Match exactly the spelling gofeed/goxpp honors: a lowercase, adjacent
	// "encoding=" (it does NOT recognize a case-variant or whitespace-around-'='
	// form \u2014 verified: those raw-byte feeds fail "invalid UTF-8"). The check is
	// deliberately congruent with the parser: it returns true only when gofeed
	// itself will transcode, so we skip pre-wrapping only then and never
	// double-transcode. Any spelling gofeed ignores falls through to charset
	// pre-transcoding, which is exactly what such a feed needs.
	return strings.Contains(s[:end], "encoding=")
}

// EntryHash computes a stable content hash for an entry. Exposed so the service
// can set Entry.Hash consistently.
func EntryHash(title, content, summary string) string {
	return hashStr(joinFields(title, content, summary))
}

// joinFields concatenates parts into a single injection-proof string by
// length-prefixing each part ("<len>:<part>"). Unlike a bare delimiter join,
// no arrangement of delimiter characters inside the parts can make two distinct
// tuples produce the same output, so hashes derived from it never falsely
// collide (e.g. ("a","b|c") and ("a|b","c")).
func joinFields(parts ...string) string {
	var b strings.Builder
	for _, p := range parts {
		b.WriteString(strconv.Itoa(len(p)))
		b.WriteByte(':')
		b.WriteString(p)
	}
	return b.String()
}

// feedTTL derives the publisher's minimum poll interval from RSS <ttl> (scanned
// from the raw bytes; the universal parser drops it) and the syndication module
// (sy:updatePeriod / sy:updateFrequency, available via Extensions). The larger
// of the two wins. Atom/JSON have no standard TTL -> 0.
func feedTTL(f *gofeed.Feed, data []byte) time.Duration {
	var ttl time.Duration
	if f.FeedType == "rss" {
		if m := rssTTLMinutes(data); m > 0 {
			ttl = time.Duration(m) * time.Minute
		}
	}
	if sy := syInterval(f); sy > ttl {
		ttl = sy
	}
	return ttl
}

// rssTTLMinutes returns the channel-level RSS <ttl> value (minutes), or 0.
// A targeted token scan — cheaper than re-running the full feed parser. <ttl> is
// a core-RSS channel element that precedes the items, so the scan stops at the
// first item/entry (bounding the work and ignoring any item-level <ttl>) and
// skips namespaced elements (e.g. media:ttl) that merely share the local name.
func rssTTLMinutes(data []byte) int {
	dec := xml.NewDecoder(bytes.NewReader(data))
	for {
		tok, err := dec.Token()
		if err != nil {
			return 0
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch se.Name.Local {
		case "item", "entry": // ttl is channel-level, before items — stop scanning
			return 0
		case "ttl":
			if se.Name.Space != "" { // foreign namespace, not core RSS <ttl>
				continue
			}
			var v string
			if dec.DecodeElement(&v, &se) == nil {
				if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
					return n
				}
			}
			return 0
		}
	}
}

// syInterval converts sy:updatePeriod / sy:updateFrequency to a duration, or 0.
func syInterval(f *gofeed.Feed) time.Duration {
	sy := f.Extensions["sy"]
	if sy == nil {
		return 0
	}
	period := extValue(sy, "updatePeriod")
	if period == "" {
		return 0
	}
	freq := 1
	if fs := extValue(sy, "updateFrequency"); fs != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(fs)); err == nil && n > 0 {
			freq = n
		}
	}
	var base time.Duration
	switch strings.ToLower(strings.TrimSpace(period)) {
	case "hourly":
		base = time.Hour
	case "daily":
		base = 24 * time.Hour
	case "weekly":
		base = 7 * 24 * time.Hour
	case "monthly":
		base = 30 * 24 * time.Hour
	case "yearly":
		base = 365 * 24 * time.Hour
	default:
		return 0
	}
	return base / time.Duration(freq)
}

func extValue(m map[string][]ext.Extension, key string) string {
	if v := m[key]; len(v) > 0 {
		return v[0].Value
	}
	return ""
}

func (p *Parser) Discover(data []byte, pageURL string) ([]string, error) {
	doc, err := html.Parse(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("html parse: %w", err)
	}
	base, _ := url.Parse(pageURL)
	var out []string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "link" {
			var rel, typ, href string
			for _, a := range n.Attr {
				switch strings.ToLower(a.Key) {
				case "rel":
					rel = strings.ToLower(a.Val)
				case "type":
					typ = strings.ToLower(a.Val)
				case "href":
					href = a.Val
				}
			}
			if rel == "alternate" && href != "" &&
				(typ == "application/rss+xml" || typ == "application/atom+xml" ||
					typ == "application/json" || typ == "application/feed+json") {
				out = append(out, resolve(base, href))
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return out, nil
}

// commentsURL turns a raw RSS <comments> value into a resolvable URL. Unlike
// <link>, a bare word here ("12", "yes") is far more likely comment-COUNT
// misuse than a relative link — resolving it against the feed base would mint
// a plausible-looking URL to a nonexistent page that the ingest http(s) guard
// then happily accepts. So only deliberate relative forms (rooted, dot-rooted,
// fragment, query) resolve against the base; anything else must already be
// absolute. Scheme filtering stays at the ingest boundary (core.httpURL).
func commentsURL(base *url.URL, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if u.IsAbs() {
		return raw
	}
	for _, p := range []string{"/", "./", "../", "#", "?"} {
		if strings.HasPrefix(raw, p) {
			return resolve(base, raw)
		}
	}
	return ""
}

func resolve(base *url.URL, ref string) string {
	if base == nil || ref == "" {
		return ref
	}
	u, err := url.Parse(ref)
	if err != nil {
		return ref
	}
	return base.ResolveReference(u).String()
}

func hashStr(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
