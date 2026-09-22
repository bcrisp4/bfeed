package core_test

import (
	"encoding/xml"
	"strings"
	"testing"

	"github.com/bcrisp4/bfeed/internal/core"
)

func TestMarshalOPML(t *testing.T) {
	newsID := core.ID(4)
	techID := core.ID(7)
	feeds := []*core.Feed{
		{FeedURL: "https://example.test/unfiled?x=1&y=2", Title: "Unfiled", Disabled: true, ETag: "private-etag"},
		{FeedURL: "https://example.test/news", SiteURL: "https://example.test/?a=1&b=2", Title: "Old name", UserTitle: "News <&> \"6\"", CategoryID: &newsID, LastError: "private-error"},
		{FeedURL: "https://example.test/atom", Title: "Atom", CategoryID: &newsID},
	}
	cats := []*core.Category{{ID: newsID, Title: "World & local"}, {ID: techID, Title: "Empty"}}

	data, err := core.MarshalOPML(feeds, cats)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), xml.Header) {
		t.Fatalf("missing XML declaration: %s", data)
	}
	var doc struct {
		XMLName xml.Name `xml:"opml"`
		Version string   `xml:"version,attr"`
		Head    struct {
			Title string `xml:"title"`
		} `xml:"head"`
		Body struct {
			Outlines []struct {
				Text     string `xml:"text,attr"`
				Title    string `xml:"title,attr"`
				Type     string `xml:"type,attr"`
				XMLURL   string `xml:"xmlUrl,attr"`
				HTMLURL  string `xml:"htmlUrl,attr"`
				Outlines []struct {
					Text    string `xml:"text,attr"`
					Title   string `xml:"title,attr"`
					Type    string `xml:"type,attr"`
					XMLURL  string `xml:"xmlUrl,attr"`
					HTMLURL string `xml:"htmlUrl,attr"`
				} `xml:"outline"`
			} `xml:"outline"`
		} `xml:"body"`
	}
	if err := xml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("invalid OPML: %v\n%s", err, data)
	}
	if doc.Version != "2.0" || doc.Head.Title == "" {
		t.Fatalf("missing OPML version or title: %+v", doc)
	}
	if len(doc.Body.Outlines) != 3 {
		t.Fatalf("root outlines: %+v", doc.Body.Outlines)
	}
	news := doc.Body.Outlines[0]
	if news.Text != "World & local" || news.Title != news.Text || news.Type != "" || news.XMLURL != "" || len(news.Outlines) != 2 {
		t.Fatalf("category outline: %+v", news)
	}
	if got := news.Outlines[0]; got.Text != "News <&> \"6\"" || got.Title != got.Text || got.XMLURL != "https://example.test/news" || got.HTMLURL != "https://example.test/?a=1&b=2" || got.Type != "rss" {
		t.Fatalf("renamed feed outline: %+v", got)
	}
	if got := news.Outlines[1]; got.Text != "Atom" || got.Type != "rss" || got.HTMLURL != "" {
		t.Fatalf("second feed outline: %+v", got)
	}
	if got := doc.Body.Outlines[1]; got.Text != "Empty" || len(got.Outlines) != 0 {
		t.Fatalf("empty category: %+v", got)
	}
	if got := doc.Body.Outlines[2]; got.XMLURL != "https://example.test/unfiled?x=1&y=2" || got.Type != "rss" || got.HTMLURL != "" {
		t.Fatalf("uncategorised feed: %+v", got)
	}
	if strings.Contains(string(data), "private-") || strings.Contains(string(data), "Old name") {
		t.Fatalf("export leaks internal data: %s", data)
	}
}

func TestMarshalOPMLOmitsURLUserinfo(t *testing.T) {
	feeds := []*core.Feed{{
		FeedURL: "https://name:basicpass@example.test/feed?token=querysecret&lang=en",
		SiteURL: "https://reader:sitepass@example.test/",
		Title:   "Private feed",
	}}
	data, err := core.MarshalOPML(feeds, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "basicpass") || strings.Contains(string(data), "sitepass") || strings.Contains(string(data), "name:") {
		t.Fatalf("userinfo leaked: %s", data)
	}
	var doc struct {
		Body struct {
			Outlines []struct {
				XMLURL  string `xml:"xmlUrl,attr"`
				HTMLURL string `xml:"htmlUrl,attr"`
			} `xml:"outline"`
		} `xml:"body"`
	}
	if err := xml.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Body.Outlines) != 1 || doc.Body.Outlines[0].XMLURL != "https://example.test/feed?token=querysecret&lang=en" || doc.Body.Outlines[0].HTMLURL != "https://example.test/" {
		t.Fatalf("URLs after userinfo removal: %+v", doc.Body.Outlines)
	}
}

func TestMarshalOPMLOmitsInvalidSiteURL(t *testing.T) {
	for _, siteURL := range []string{"https://name:secret@example.test/%zz", "javascript:alert(1)"} {
		t.Run(siteURL, func(t *testing.T) {
			data, err := core.MarshalOPML([]*core.Feed{{FeedURL: "https://example.test/feed", Title: "Feed", SiteURL: siteURL}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(data), "htmlUrl") || strings.Contains(string(data), "secret") || strings.Contains(string(data), "javascript:") {
				t.Fatalf("invalid site URL in OPML: %s", data)
			}
		})
	}
}

func TestMarshalOPMLKeepsUppercaseSchemes(t *testing.T) {
	data, err := core.MarshalOPML([]*core.Feed{{
		FeedURL: "HTTPS://example.test/feed?format=atom",
		SiteURL: "HTTP://example.test/",
		Title:   "Feed",
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Body struct {
			Outlines []struct {
				XMLURL  string `xml:"xmlUrl,attr"`
				HTMLURL string `xml:"htmlUrl,attr"`
			} `xml:"outline"`
		} `xml:"body"`
	}
	if err := xml.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Body.Outlines) != 1 || doc.Body.Outlines[0].XMLURL != "HTTPS://example.test/feed?format=atom" || doc.Body.Outlines[0].HTMLURL != "HTTP://example.test/" {
		t.Fatalf("uppercase URL schemes lost: %+v", doc.Body.Outlines)
	}
}

func TestMarshalOPMLReplacesInvalidXMLCharacters(t *testing.T) {
	catID := core.ID(1)
	data, err := core.MarshalOPML(
		[]*core.Feed{{FeedURL: "https://example.test/feed", Title: "Bad\x00name\ufffe", CategoryID: &catID}},
		[]*core.Category{{ID: catID, Title: "My\x01 feeds"}},
	)
	if err != nil {
		t.Fatalf("export failed on invalid XML characters: %v", err)
	}
	var doc struct {
		Body struct {
			Outlines []struct {
				Text     string `xml:"text,attr"`
				Outlines []struct {
					Text string `xml:"text,attr"`
				} `xml:"outline"`
			} `xml:"outline"`
		} `xml:"body"`
	}
	if err := xml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("invalid OPML: %v\n%s", err, data)
	}
	if got := doc.Body.Outlines[0]; got.Text != "My\ufffd feeds" || len(got.Outlines) != 1 || got.Outlines[0].Text != "Bad\ufffdname\ufffd" {
		t.Fatalf("invalid characters survive in OPML: %+v", got)
	}
}

func TestMarshalOPMLEmpty(t *testing.T) {
	data, err := core.MarshalOPML(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Body struct {
			Outlines []struct{} `xml:"outline"`
		} `xml:"body"`
	}
	if err := xml.Unmarshal(data, &doc); err != nil || len(doc.Body.Outlines) != 0 {
		t.Fatalf("empty export is not valid OPML: %v\n%s", err, data)
	}
}
