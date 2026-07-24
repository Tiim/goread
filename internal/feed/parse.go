// Package feed parses RSS and Atom feed content (via gofeed) and
// synchronizes the parsed items into the database using the GUID -> Link ->
// content-hash identity fallback chain.
package feed

import (
	"bytes"
	"fmt"

	"github.com/mmcdole/gofeed"
)

// Parse parses raw RSS or Atom feed content into gofeed's universal Feed
// representation. gofeed auto-detects the underlying format (RSS 0.9x/1.0/2.0
// or Atom) from the document itself.
func Parse(data []byte) (*gofeed.Feed, error) {
	parser := gofeed.NewParser()
	// Retains the source rss.Feed/atom.Feed on the result so TTL can inspect
	// the RSS <ttl> element, which gofeed's universal Feed does not expose.
	parser.KeepOriginalFeed = true
	parsed, err := parser.Parse(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("parse feed: %w", err)
	}
	return parsed, nil
}
