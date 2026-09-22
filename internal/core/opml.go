package core

import (
	"encoding/xml"
	"net/url"
)

type opmlDocument struct {
	XMLName xml.Name `xml:"opml"`
	Version string   `xml:"version,attr"`
	Head    opmlHead `xml:"head"`
	Body    opmlBody `xml:"body"`
}

type opmlHead struct {
	Title string `xml:"title"`
}

type opmlBody struct {
	Outlines []opmlOutline `xml:"outline"`
}

type opmlOutline struct {
	Text     string        `xml:"text,attr"`
	Title    string        `xml:"title,attr"`
	Type     string        `xml:"type,attr,omitempty"`
	XMLURL   string        `xml:"xmlUrl,attr,omitempty"`
	HTMLURL  string        `xml:"htmlUrl,attr,omitempty"`
	Outlines []opmlOutline `xml:"outline,omitempty"`
}

// MarshalOPML serializes feeds and categories as OPML 2.0. Callers must pass
// only the user's own rows. URLs retain query strings but omit userinfo and invalid URLs.
func MarshalOPML(feeds []*Feed, categories []*Category) ([]byte, error) {
	doc := opmlDocument{Version: "2.0", Head: opmlHead{Title: "bfeed subscriptions"}}
	byCategory := make(map[ID][]opmlOutline, len(categories))
	known := make(map[ID]bool, len(categories))
	for _, c := range categories {
		known[c.ID] = true
	}
	var uncategorised []opmlOutline
	for _, f := range feeds {
		title := f.DisplayTitle()
		outline := opmlOutline{Text: title, Title: title, Type: "rss", XMLURL: opmlURL(f.FeedURL), HTMLURL: opmlURL(f.SiteURL)}
		if f.CategoryID != nil && known[*f.CategoryID] {
			byCategory[*f.CategoryID] = append(byCategory[*f.CategoryID], outline)
		} else {
			uncategorised = append(uncategorised, outline)
		}
	}
	for _, c := range categories {
		doc.Body.Outlines = append(doc.Body.Outlines, opmlOutline{
			Text: c.Title, Title: c.Title, Outlines: byCategory[c.ID],
		})
	}
	doc.Body.Outlines = append(doc.Body.Outlines, uncategorised...)
	data, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(append([]byte(xml.Header), data...), '\n'), nil
}

func opmlURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	if u.User == nil {
		return raw
	}
	u.User = nil
	return u.String()
}
