package parse

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/mmcdole/gofeed"
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
