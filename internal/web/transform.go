package web

import (
	"bytes"
	"strings"
	"unicode"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// transformContent applies the reader's render-time transforms to
// already-sanitised entry HTML in one parse/walk/render pass:
//
//  1. An image-only <p> — whitespace-only text nodes plus exactly one <img>,
//     or one <a> that is itself image-only — is renamed to <figure> so the
//     full-bleed CSS slot (figure:has(img), app.css) widens it (#90). CSS
//     cannot express this: selectors can't see text nodes. Stored content is
//     untouched; the promotion is display-layer only.
//  2. Every http(s) <img src> is rewritten to rewrite(src) (a signed
//     same-origin proxy URL) when rewrite is non-nil. data: and other-scheme
//     srcs are left untouched.
//
// The input is already safe; this only renames nodes (p and figure are both
// sanitizer-allowed elements) and swaps attribute values — it adds no new
// nodes or attributes and relies on no unsanitized input. Parsed as a body fragment so no
// <html>/<head>/<body> wrapper is added. On parse/render error the input is
// returned unchanged.
func transformContent(in string, rewrite func(string) string) string {
	nodes, err := html.ParseFragment(strings.NewReader(in), &html.Node{
		Type:     html.ElementNode,
		Data:     "body",
		DataAtom: atom.Body,
	})
	if err != nil {
		return in
	}
	var buf bytes.Buffer
	for _, n := range nodes {
		transformWalk(n, rewrite)
		if err := html.Render(&buf, n); err != nil {
			return in
		}
	}
	return buf.String()
}

func transformWalk(n *html.Node, rewrite func(string) string) {
	if n.Type == html.ElementNode {
		switch n.DataAtom {
		case atom.P:
			if isImageOnly(n, true) {
				n.Data = "figure"
				n.DataAtom = atom.Figure
			}
		case atom.Img:
			if rewrite != nil {
				for i, a := range n.Attr {
					if a.Key == "src" && (strings.HasPrefix(a.Val, "http://") || strings.HasPrefix(a.Val, "https://")) {
						n.Attr[i].Val = rewrite(a.Val)
					}
				}
			}
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		transformWalk(c, rewrite)
	}
}

// isImageOnly reports whether n's children are whitespace-only text nodes
// (unicode.IsSpace — includes NBSP) plus exactly one element child: an <img>,
// or, when allowLink, an <a> that is itself image-only (the WordPress
// click-to-enlarge shape <p><a><img></a></p>; one level only).
func isImageOnly(n *html.Node, allowLink bool) bool {
	var el *html.Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		switch c.Type {
		case html.TextNode:
			if strings.TrimFunc(c.Data, unicode.IsSpace) != "" {
				return false
			}
		case html.ElementNode:
			if el != nil {
				return false
			}
			el = c
		case html.CommentNode: // sanitiser strips these, but harmless
		default:
			return false
		}
	}
	if el == nil {
		return false
	}
	if el.DataAtom == atom.Img {
		return true
	}
	return allowLink && el.DataAtom == atom.A && isImageOnly(el, false)
}
