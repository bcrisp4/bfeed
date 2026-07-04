package web

import (
	"html"
	"regexp"
	"strings"

	"github.com/bcrisp4/bfeed/internal/core"
)

var (
	tagRE = regexp.MustCompile(`<[^>]*>`)
	urlRE = regexp.MustCompile(`(?:https?://|www\.)\S+`)
)

const (
	// maxSummaryScan bounds how much HTML is inspected per source. A list blurb
	// is CSS-clamped to ~2 lines, so a prefix is plenty and avoids a full regex
	// pass over a large scraped article on every row of every list render.
	maxSummaryScan = 2048
	// A preview is rejected when URLs make up more than maxLinkDensity of its
	// text, or it carries fewer than minPreviewWords of prose. Calibrated against
	// real feeds: link/metadata-dump summaries (e.g. hnrss "Article URL: … Comments
	// URL: …") score 0.6–0.8 density; genuine prose scores 0.0–0.05.
	maxLinkDensity  = 0.4
	minPreviewWords = 5
	// maxPreviewChars bounds the blurb sent to the client. The CSS clamps the row
	// to ~2 lines, so shipping a whole scraped article (× 50 rows) is wasted bytes.
	maxPreviewChars = 280
)

// htmlToText converts already-sanitised HTML to plain text: strip tags, decode
// entities, collapse whitespace. Decoding matters because the template
// re-escapes the result — leaving entities encoded would double-escape them
// (e.g. "AT&amp;T" would otherwise display to the user as the literal "AT&amp;T").
func htmlToText(s string) string {
	s = tagRE.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	return strings.Join(strings.Fields(s), " ")
}

// trimScanWindow makes a (possibly byte-truncated) scan window safe for htmlToText:
// it drops an unterminated trailing tag (a '<' with no following '>', which tagRE
// cannot strip so it would leak as literal text or inflate goodPreview's link
// density) and any trailing invalid UTF-8 (a rune split by the cut). Stored content
// is always sanitised, so a raw '<' is always a tag-opener, never literal text —
// trimming from the last unbalanced '<' can't eat real prose. '<' (0x3C) is never a
// UTF-8 continuation byte, so the two steps are order-independent.
func trimScanWindow(s string) string {
	if lt := strings.LastIndexByte(s, '<'); lt > strings.LastIndexByte(s, '>') {
		s = s[:lt]
	}
	return strings.ToValidUTF8(s, "")
}

// goodPreview reports whether plain text reads as real prose worth showing as a
// list blurb — not a link/URL dump and not a bare "read more" stub. It judges
// the text itself, not the source feed, so it generalises across sites.
func goodPreview(text string) bool {
	if text == "" {
		return false
	}
	urlChars := 0
	for _, u := range urlRE.FindAllString(text, -1) {
		urlChars += len(u)
	}
	if float64(urlChars)/float64(len(text)) > maxLinkDensity {
		return false
	}
	return len(strings.Fields(urlRE.ReplaceAllString(text, " "))) >= minPreviewWords
}

// summaryText derives a short list-row blurb, preferring the feed's hand-written
// Summary and falling back to the (often scraped, full-text) Content. Sources
// that are only links/metadata are skipped, so a feed whose summary is a bare
// link (e.g. Hacker News) shows the article's opening instead — and nothing only
// when neither source carries real prose.
func summaryText(e *core.Entry) string {
	for _, src := range [2]string{e.Summary, e.Content} {
		if strings.TrimSpace(src) == "" {
			continue
		}
		scan := src
		if len(scan) > maxSummaryScan {
			scan = scan[:maxSummaryScan]
		}
		// Applied unconditionally: the source may already arrive cut at a byte
		// boundary from the store's substr(content,1,2048) list projection, so a
		// dangling tag/rune can be present even when we didn't slice here.
		scan = trimScanWindow(scan)
		if text := htmlToText(scan); goodPreview(text) {
			return truncatePreview(text)
		}
	}
	return ""
}

// truncatePreview trims a blurb to maxPreviewChars at a word boundary, adding an
// ellipsis when it cuts, so list responses stay small regardless of source length.
func truncatePreview(s string) string {
	r := []rune(s)
	if len(r) <= maxPreviewChars {
		return s
	}
	cut := string(r[:maxPreviewChars])
	if i := strings.LastIndexByte(cut, ' '); i > 0 {
		cut = cut[:i]
	}
	return cut + "…"
}
