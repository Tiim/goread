// Package sanitize strips article HTML down to a safe subset before it is
// rendered inside the sandboxed article iframe (see docs/spec.md "HTML
// Rendering"): no scripts, no forms, no inline event handlers, no unsafe
// elements such as iframe/object/embed/style/link.
package sanitize

import (
	"strings"

	"github.com/microcosm-cc/bluemonday"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

var policy = newPolicy()

func newPolicy() *bluemonday.Policy {
	p := bluemonday.UGCPolicy()

	// UGCPolicy already strips <script>, <style>, <iframe>, <object>,
	// <embed>, forms, and any "on*" event handler attributes. We only add
	// what article rendering additionally needs: standard link handling
	// and basic table support for feed content.
	p.AllowStandardURLs()
	p.RequireNoFollowOnLinks(true)
	p.RequireNoReferrerOnLinks(true)
	p.AddTargetBlankToFullyQualifiedLinks(true)
	p.AllowAttrs("colspan", "rowspan").OnElements("td", "th")
	p.AllowImages()
	p.AllowAttrs("loading").OnElements("img")

	return p
}

// HTML sanitizes raw article HTML, removing scripts, event handlers, forms,
// and any element/attribute not on the allow list.
func HTML(raw string) string {
	return policy.Sanitize(raw)
}

// RewriteImageSrcs rewrites the src attribute of every <img> in sanitized
// HTML using rewrite, so the caller can route remote images through a local
// proxy instead of letting the browser fetch them directly. sanitizedHTML
// must already have passed through HTML.
func RewriteImageSrcs(sanitizedHTML string, rewrite func(src string) string) (string, error) {
	nodes, err := html.ParseFragment(strings.NewReader(sanitizedHTML), &html.Node{
		Type:     html.ElementNode,
		Data:     "div",
		DataAtom: atom.Div,
	})
	if err != nil {
		return "", err
	}

	for _, n := range nodes {
		rewriteImgSrcs(n, rewrite)
	}

	var buf strings.Builder
	for _, n := range nodes {
		if err := html.Render(&buf, n); err != nil {
			return "", err
		}
	}
	return buf.String(), nil
}

func rewriteImgSrcs(n *html.Node, rewrite func(src string) string) {
	if n.Type == html.ElementNode && n.DataAtom == atom.Img {
		for i, attr := range n.Attr {
			if attr.Key == "src" {
				n.Attr[i].Val = rewrite(attr.Val)
			}
		}
		// Drop srcset/data-src style attributes: they'd let the browser
		// bypass the proxy and fetch a remote URL directly.
		filtered := n.Attr[:0]
		for _, attr := range n.Attr {
			if attr.Key == "srcset" {
				continue
			}
			filtered = append(filtered, attr)
		}
		n.Attr = filtered
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		rewriteImgSrcs(c, rewrite)
	}
}
