// Package opml implements parsing and generation of OPML feed subscription
// lists (per http://opml.org/spec2.opml), used for GoRead's import/export
// feature.
package opml

import (
	"encoding/xml"
	"fmt"
	"io"
	"strings"
	"unicode"
)

// Feed is a single subscription entry extracted from (or destined for) an
// OPML document.
type Feed struct {
	Title   string
	FeedURL string
	SiteURL string
	// Folder is the feed's folder path. Nested OPML outline groups are
	// flattened into a single string, joined with "/", since GoRead stores
	// folder as a flat string column rather than a true hierarchy.
	Folder string
}

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
	Title    string        `xml:"title,attr,omitempty"`
	Type     string        `xml:"type,attr,omitempty"`
	XMLURL   string        `xml:"xmlUrl,attr,omitempty"`
	HTMLURL  string        `xml:"htmlUrl,attr,omitempty"`
	Outlines []opmlOutline `xml:"outline,omitempty"`
}

// Parse reads an OPML document and returns its feed subscriptions, in
// document order, with folder hierarchy flattened into Feed.Folder.
// Outlines without an xmlUrl attribute are treated as folder groupings and
// descended into rather than imported as feeds.
func Parse(r io.Reader) ([]Feed, error) {
	var doc opmlDocument
	if err := xml.NewDecoder(r).Decode(&doc); err != nil {
		return nil, fmt.Errorf("opml: decode: %w", err)
	}
	var feeds []Feed
	walkOutlines(doc.Body.Outlines, "", &feeds)
	return feeds, nil
}

func walkOutlines(outlines []opmlOutline, folder string, feeds *[]Feed) {
	for _, o := range outlines {
		if o.XMLURL != "" {
			*feeds = append(*feeds, Feed{
				Title:   outlineName(o),
				FeedURL: o.XMLURL,
				SiteURL: o.HTMLURL,
				Folder:  folder,
			})
			continue
		}

		if inner, ok := unwrapFeedOutline(o); ok {
			*feeds = append(*feeds, Feed{
				Title:   outlineName(inner),
				FeedURL: inner.XMLURL,
				SiteURL: inner.HTMLURL,
				Folder:  folder,
			})
			continue
		}

		childFolder := joinFolder(folder, outlineName(o))
		walkOutlines(o.Outlines, childFolder, feeds)
	}
}

// unwrapFeedOutline detects Thunderbird's OPML export quirk of wrapping
// every single feed subscription in its own intermediate outline named
// (almost) identically to the feed itself - producing a redundant
// folder/podcastname/podcastname nesting rather than folder/podcastname.
// If o is such a wrapper (its only child is a feed leaf whose name matches
// o's own name closely enough - the two commonly drift slightly, e.g.
// "Weekly Comic Often updated" vs "Weekly Comic. Often updated.", or a
// shortened form like "Nickname" vs "Nickname's Extended Blog Title" - the
// inner feed outline is returned directly so no extra folder level is
// introduced for it. A wrapper with more than one child, or whose name
// doesn't relate to its child's, is left alone and treated as a genuine
// folder.
func unwrapFeedOutline(o opmlOutline) (opmlOutline, bool) {
	if len(o.Outlines) != 1 {
		return opmlOutline{}, false
	}
	inner := o.Outlines[0]
	if inner.XMLURL == "" || len(inner.Outlines) != 0 {
		return opmlOutline{}, false
	}

	outerName := normalizeName(outlineName(o))
	innerName := normalizeName(outlineName(inner))
	if outerName == "" || innerName == "" {
		return opmlOutline{}, false
	}
	if outerName != innerName && !strings.Contains(innerName, outerName) && !strings.Contains(outerName, innerName) {
		return opmlOutline{}, false
	}
	return inner, true
}

func outlineName(o opmlOutline) string {
	if o.Title != "" {
		return o.Title
	}
	return o.Text
}

// normalizeName lowercases s and strips everything but letters/digits, so
// names that differ only in punctuation or whitespace (e.g. a trailing
// period, an em-dash vs. a space) compare equal.
func normalizeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

func joinFolder(parent, name string) string {
	switch {
	case parent == "":
		return name
	case name == "":
		return parent
	default:
		return parent + "/" + name
	}
}

// Generate writes feeds as an OPML 2.0 document, grouping feeds into one
// outline per distinct Folder value (feeds with an empty Folder are written
// at the top level, ungrouped).
func Generate(w io.Writer, feeds []Feed) error {
	doc := opmlDocument{
		Version: "2.0",
		Head:    opmlHead{Title: "GoRead Subscriptions"},
	}

	groups := map[string]*opmlOutline{}
	var order []string
	for _, f := range feeds {
		group, ok := groups[f.Folder]
		if !ok {
			group = &opmlOutline{}
			groups[f.Folder] = group
			order = append(order, f.Folder)
		}
		group.Outlines = append(group.Outlines, opmlOutline{
			Text:    f.Title,
			Title:   f.Title,
			Type:    "rss",
			XMLURL:  f.FeedURL,
			HTMLURL: f.SiteURL,
		})
	}

	for _, folder := range order {
		if folder == "" {
			doc.Body.Outlines = append(doc.Body.Outlines, groups[folder].Outlines...)
			continue
		}
		doc.Body.Outlines = append(doc.Body.Outlines, opmlOutline{
			Text:     folder,
			Title:    folder,
			Outlines: groups[folder].Outlines,
		})
	}

	if _, err := io.WriteString(w, xml.Header); err != nil {
		return fmt.Errorf("opml: write header: %w", err)
	}
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return fmt.Errorf("opml: encode: %w", err)
	}
	if _, err := io.WriteString(w, "\n"); err != nil {
		return fmt.Errorf("opml: write trailing newline: %w", err)
	}
	return nil
}
