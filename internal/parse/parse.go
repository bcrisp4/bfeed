package parse

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mmcdole/gofeed"
	ext "github.com/mmcdole/gofeed/extensions"
	"golang.org/x/net/html"

	"github.com/bcrisp4/bfeed/internal/core"
)

type Parser struct{ fp *gofeed.Parser }

func New() *Parser { return &Parser{fp: gofeed.NewParser()} }

var _ core.FeedParser = (*Parser)(nil)

func (p *Parser) Parse(data []byte, feedURL string) (*core.ParsedFeed, error) {
	f, err := p.fp.Parse(bytes.NewReader(data))
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

// rssTTLMinutes returns the first channel-level <ttl> value (minutes), or 0.
// A targeted token scan — cheaper than re-running the full feed parser.
func rssTTLMinutes(data []byte) int {
	dec := xml.NewDecoder(bytes.NewReader(data))
	for {
		tok, err := dec.Token()
		if err != nil {
			return 0
		}
		if se, ok := tok.(xml.StartElement); ok && se.Name.Local == "ttl" {
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
