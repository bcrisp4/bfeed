package web

import (
	"bytes"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// proxifyImages rewrites every http(s) <img src> in already-sanitised HTML to
// rewrite(src) (a signed same-origin proxy URL). data: and other-scheme srcs are
// left untouched. The input is already safe; this only swaps attribute values
// and never re-introduces markup. Parsed as a body fragment so no
// <html>/<head>/<body> wrapper is added to the output.
func proxifyImages(in string, rewrite func(string) string) string {
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
		proxifyWalk(n, rewrite)
		if err := html.Render(&buf, n); err != nil {
			return in
		}
	}
	return buf.String()
}

func proxifyWalk(n *html.Node, rewrite func(string) string) {
	if n.Type == html.ElementNode && n.Data == "img" {
		for i, a := range n.Attr {
			if a.Key == "src" && (strings.HasPrefix(a.Val, "http://") || strings.HasPrefix(a.Val, "https://")) {
				n.Attr[i].Val = rewrite(a.Val)
			}
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		proxifyWalk(c, rewrite)
	}
}
