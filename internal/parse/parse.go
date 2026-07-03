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

	"github.com/mmcdole/gofeed"
	ext "github.com/mmcdole/gofeed/extensions"
	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"

	"github.com/bcrisp4/bfeed/internal/core"
)

type Parser struct{ fp *gofeed.Parser }

func New() *Parser { return &Parser{fp: gofeed.NewParser()} }

var _ core.FeedParser = (*Parser)(nil)

func (p *Parser) Parse(data []byte, contentType, feedURL string) (*core.ParsedFeed, error) {
	f, err := p.fp.Parse(decodeReader(data, contentType))
	if err != nil {
		return nil, fmt.Errorf("gofeed parse: %w", err)
	}
	base, _ := url.Parse(feedURL)
	out := &core.ParsedFeed{Title: f.Title, Description: f.Description, SiteURL: resolve(base, f.Link)}
	out.TTL = feedTTL(f, data)
	for _, it := range f.Items {
		link := resolve(base, it.Link)
		guid := it.GUID
		if guid == "" {
			guid = hashStr(link + "|" + it.Title)
		}
		var pub time.Time
		if it.PublishedParsed != nil {
			pub = it.PublishedParsed.UTC()
		} else if it.UpdatedParsed != nil {
			pub = it.UpdatedParsed.UTC()
		}
		author := ""
		if it.Author != nil {
			author = it.Author.Name
		}
		content := it.Content
		summary := it.Description
		out.Entries = append(out.Entries, core.ParsedEntry{
			GUID:        guid,
			URL:         link,
			Title:       it.Title,
			Author:      author,
			Content:     content,
			Summary:     summary,
			PublishedAt: pub,
			Hash:        EntryHash(it.Title, content, summary),
		})
	}
	return out, nil
}

// decodeReader returns a UTF-8 reader for the feed bytes. gofeed only transcodes
// when the XML declaration names a non-UTF-8 encoding, so a feed whose charset
// lives only in the HTTP Content-Type (RFC 3023) fails as "invalid UTF-8". We
// bridge that gap with charset.NewReader, which honors the HTTP charset, a BOM,
// and content sniffing. Crucially we do this ONLY when the bytes carry no in-band
// encoding declaration: if we pre-transcoded a feed that also declares its
// encoding, encoding/xml would re-invoke gofeed's CharsetReader on the now-UTF-8
// stream and double-transcode it into mojibake. Falls back to the raw bytes if a
// charset reader can't be built.
func decodeReader(data []byte, contentType string) io.Reader {
	if hasXMLEncodingDecl(data) {
		return bytes.NewReader(data)
	}
	if r, err := charset.NewReader(bytes.NewReader(data), contentType); err == nil {
		return r
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
	return hashStr(title + "|" + content + "|" + summary)
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
				(typ == "application/rss+xml" || typ == "application/atom+xml" || typ == "application/json") {
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
