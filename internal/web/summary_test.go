package web

import (
	"testing"

	"github.com/bcrisp4/bfeed/internal/core"
)

// A realistic hnrss-style summary: labels + two long URLs, little prose.
const hnDump = `<p>Article URL: <a href="https://example.com/the-article">https://example.com/the-article</a> ` +
	`Comments URL: <a href="https://news.ycombinator.com/item?id=1">https://news.ycombinator.com/item?id=1</a> ` +
	`Points: 35 # Comments: 10</p>`

func TestSummaryText(t *testing.T) {
	const articleBody = "<p>The opening paragraph of the scraped article, with plenty of real words to serve as a preview here.</p>"

	cases := []struct {
		name string
		e    *core.Entry
		want string
	}{
		{
			"prefers a real summary teaser",
			&core.Entry{Summary: "<p>A genuine teaser sentence describing what this post is about.</p>", Content: articleBody},
			"A genuine teaser sentence describing what this post is about.",
		},
		{
			// HN: link-dump summary is skipped in favour of the scraped article body.
			"link-dump summary falls back to content",
			&core.Entry{Summary: hnDump, Content: articleBody},
			"The opening paragraph of the scraped article, with plenty of real words to serve as a preview here.",
		},
		{
			"empty summary falls back to content",
			&core.Entry{Summary: "", Content: articleBody},
			"The opening paragraph of the scraped article, with plenty of real words to serve as a preview here.",
		},
		{
			// Entities decoded so the template re-escapes once, not twice.
			"decodes entities",
			&core.Entry{Summary: "<p>Tom &amp; Jerry &mdash; and a few more words to clear the floor.</p>"},
			"Tom & Jerry — and a few more words to clear the floor.",
		},
		{
			"nothing when neither source is prose",
			&core.Entry{Summary: hnDump, Content: ""},
			"",
		},
		{
			// A bare link stub (0 URLs in text, but only one word) is suppressed.
			"bare-link stub suppressed",
			&core.Entry{Summary: `<a href="https://x/1">Comments</a>`, Content: ""},
			"",
		},
		{"both empty", &core.Entry{}, ""},
	}
	for _, c := range cases {
		if got := summaryText(c.e); got != c.want {
			t.Errorf("%s: summaryText() = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestGoodPreview(t *testing.T) {
	cases := []struct {
		name string
		text string
		want bool
	}{
		{"prose", "A normal sentence with enough words to count as a preview.", true},
		{"empty", "", false},
		{"too few words", "Read more", false},
		{"url dominated", "Article URL: https://example.com/a/very/long/path/that/dominates Comments URL: https://news.ycombinator.com/item?id=1 Points: 5", false},
		{"one short link with prose around it", "See the original post over at https://example.com for the full write-up.", true},
	}
	for _, c := range cases {
		if got := goodPreview(c.text); got != c.want {
			t.Errorf("%s: goodPreview(%q) = %v, want %v", c.name, c.text, got, c.want)
		}
	}
}
