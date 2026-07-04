package web

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/bcrisp4/bfeed/internal/core"
)

func TestTruncatePreview(t *testing.T) {
	if got := truncatePreview("a few short words"); got != "a few short words" {
		t.Fatalf("short text should pass through, got %q", got)
	}
	long := strings.Repeat("word ", 100) // ~500 chars
	got := truncatePreview(long)
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("long text not ellipsized: %q", got)
	}
	if n := len([]rune(got)); n > maxPreviewChars+1 {
		t.Fatalf("truncated length %d exceeds cap %d", n, maxPreviewChars)
	}
}

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

// F6: the scan window can arrive cut mid-tag (from a DB substr projection or the
// byte slice) or mid-rune. trimScanWindow drops a dangling unterminated tag and any
// trailing invalid UTF-8 so the fragment neither leaks into htmlToText nor inflates
// goodPreview's link-density check.
func TestTrimScanWindow(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"unterminated tag trimmed", `Real prose here <a href="https://example.com/very/long`, "Real prose here "},
		{"closed tag kept", `Real <a href="x">prose</a>`, `Real <a href="x">prose</a>`},
		{"no tag unchanged", "plain prose", "plain prose"},
		{"trailing invalid utf8 dropped", "prose" + "\xc3", "prose"},
		{"lone lt trimmed", "prose <", "prose "},
	}
	for _, c := range cases {
		if got := trimScanWindow(c.in); got != c.want {
			t.Errorf("%s: trimScanWindow(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

// F6 integration: a valid short prose blurb followed by a dangling long-URL tag
// (the 2048 cut lands inside the href) must still preview. Without the trim, the
// dangling URL's characters dominate goodPreview's density and suppress the blurb.
func TestSummaryTextSurvivesDanglingURL(t *testing.T) {
	prose := "A short genuine opening sentence with plenty of real words to preview. "
	tag := `<a href="https://example.com/` + strings.Repeat("segment/", 300) + `">link</a>`
	e := &core.Entry{Content: prose + tag}
	if len(e.Content) <= maxSummaryScan {
		t.Fatalf("fixture too short (%d bytes); need the cut inside the tag", len(e.Content))
	}
	got := summaryText(e)
	if !strings.HasPrefix(got, "A short genuine opening sentence") {
		t.Fatalf("blurb suppressed/garbled by dangling URL: %q", got)
	}
	if strings.Contains(got, "href") || strings.Contains(got, "https") || strings.Contains(got, "<") {
		t.Fatalf("tag fragment leaked into blurb: %q", got)
	}
	if !utf8.ValidString(got) {
		t.Fatalf("blurb contains invalid UTF-8: %q", got)
	}
}
