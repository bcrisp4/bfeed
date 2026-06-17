package sanitize

import (
	"bytes"
	"net/url"
	"strings"

	"github.com/bcrisp4/bfeed/internal/core"
	"github.com/microcosm-cc/bluemonday"
	"golang.org/x/net/html"
)

type Sanitizer struct{ policy *bluemonday.Policy }

func New() *Sanitizer {
	p := bluemonday.UGCPolicy() // allows semantic content; strips script/style/handlers
	p.AllowAttrs("src", "alt", "width", "height").OnElements("img")
	p.RequireNoFollowOnLinks(true)
	return &Sanitizer{policy: p}
}

var _ core.Sanitizer = (*Sanitizer)(nil)

var trackingParams = map[string]bool{
	"fbclid": true, "gclid": true, "mc_eid": true, "igshid": true,
}

func isTrackingParam(k string) bool {
	return trackingParams[k] || strings.HasPrefix(k, "utm_")
}

// Sanitize: pre-process (drop pixels, clean links), then run bluemonday.
func (s *Sanitizer) Sanitize(htmlStr, baseURL string) string {
	cleaned := s.preprocess(htmlStr, baseURL)
	return s.policy.Sanitize(cleaned)
}

func (s *Sanitizer) preprocess(in, baseURL string) string {
	base, _ := url.Parse(baseURL)
	doc, err := html.Parse(strings.NewReader(in))
	if err != nil {
		return in
	}
	var keep func(*html.Node) bool
	var clean func(*html.Node)
	keep = func(n *html.Node) bool {
		if n.Type == html.ElementNode && n.Data == "img" {
			w, h := attr(n, "width"), attr(n, "height")
			if w == "1" && h == "1" {
				return false // tracking pixel
			}
		}
		return true
	}
	clean = func(n *html.Node) {
		for c := n.FirstChild; c != nil; {
			next := c.NextSibling
			if !keep(c) {
				n.RemoveChild(c)
				c = next
				continue
			}
			if c.Type == html.ElementNode && c.Data == "a" {
				setAttr(c, "href", cleanURL(base, attr(c, "href")))
			}
			if c.Type == html.ElementNode && (c.Data == "img" || c.Data == "a") {
				if a := attr(c, "src"); a != "" {
					setAttr(c, "src", resolveURL(base, a))
				}
			}
			clean(c)
			c = next
		}
	}
	clean(doc)
	var buf bytes.Buffer
	_ = html.Render(&buf, doc)
	return buf.String()
}

func cleanURL(base *url.URL, raw string) string {
	if raw == "" {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	q := u.Query()
	for k := range q {
		if isTrackingParam(k) {
			q.Del(k)
		}
	}
	u.RawQuery = q.Encode()
	if base != nil {
		u = base.ResolveReference(u)
	}
	return u.String()
}

func resolveURL(base *url.URL, raw string) string {
	u, err := url.Parse(raw)
	if err != nil || base == nil {
		return raw
	}
	return base.ResolveReference(u).String()
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func setAttr(n *html.Node, key, val string) {
	for i, a := range n.Attr {
		if a.Key == key {
			n.Attr[i].Val = val
			return
		}
	}
	n.Attr = append(n.Attr, html.Attribute{Key: key, Val: val})
}
