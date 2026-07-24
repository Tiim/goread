package feed

import (
	"testing"
	"time"
)

const rssSample = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:content="http://purl.org/rss/1.0/modules/content/" xmlns:dc="http://purl.org/dc/elements/1.1/">
  <channel>
    <title>Example RSS Feed</title>
    <link>https://example.com</link>
    <description>An example feed</description>
    <item>
      <title>First Post</title>
      <link>https://example.com/first</link>
      <guid isPermaLink="false">urn:uuid:1</guid>
      <description>First summary</description>
      <content:encoded><![CDATA[<p>First full content</p>]]></content:encoded>
      <pubDate>Mon, 02 Jan 2006 15:04:05 +0000</pubDate>
      <dc:creator>Jane Doe</dc:creator>
    </item>
    <item>
      <title>Second Post</title>
      <link>https://example.com/second</link>
      <description>Second summary</description>
      <pubDate>Tue, 03 Jan 2006 15:04:05 +0000</pubDate>
    </item>
  </channel>
</rss>`

const atomSample = `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>Example Atom Feed</title>
  <subtitle>An example atom feed</subtitle>
  <link rel="alternate" href="https://example.org"/>
  <entry>
    <title>Atom Entry One</title>
    <id>tag:example.org,2006:1</id>
    <link rel="alternate" href="https://example.org/one"/>
    <published>2006-01-02T15:04:05Z</published>
    <updated>2006-01-02T15:04:05Z</updated>
    <summary>Entry one summary</summary>
    <content type="html">&lt;p&gt;Entry one content&lt;/p&gt;</content>
    <author><name>John Smith</name></author>
  </entry>
</feed>`

const invalidSample = `not xml at all`

func TestParse_RSS(t *testing.T) {
	f, err := Parse([]byte(rssSample))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if f.Title != "Example RSS Feed" {
		t.Errorf("Title = %q, want %q", f.Title, "Example RSS Feed")
	}
	if f.Link != "https://example.com" {
		t.Errorf("Link = %q, want %q", f.Link, "https://example.com")
	}
	if len(f.Items) != 2 {
		t.Fatalf("len(Items) = %d, want 2", len(f.Items))
	}

	first := f.Items[0]
	if first.Title != "First Post" {
		t.Errorf("Items[0].Title = %q", first.Title)
	}
	if first.GUID != "urn:uuid:1" {
		t.Errorf("Items[0].GUID = %q", first.GUID)
	}
	if first.Content == "" {
		t.Errorf("Items[0].Content should not be empty")
	}
	if first.PublishedParsed == nil || !first.PublishedParsed.Equal(time.Date(2006, 1, 2, 15, 4, 5, 0, time.UTC)) {
		t.Errorf("Items[0].PublishedParsed = %v", first.PublishedParsed)
	}
}

func TestParse_Atom(t *testing.T) {
	f, err := Parse([]byte(atomSample))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if f.Title != "Example Atom Feed" {
		t.Errorf("Title = %q", f.Title)
	}
	if len(f.Items) != 1 {
		t.Fatalf("len(Items) = %d, want 1", len(f.Items))
	}
	entry := f.Items[0]
	if entry.GUID != "tag:example.org,2006:1" {
		t.Errorf("Items[0].GUID = %q", entry.GUID)
	}
	if entry.Link != "https://example.org/one" {
		t.Errorf("Items[0].Link = %q", entry.Link)
	}
	if entry.PublishedParsed == nil {
		t.Errorf("Items[0].PublishedParsed should not be nil")
	}
}

func TestParse_Invalid(t *testing.T) {
	if _, err := Parse([]byte(invalidSample)); err == nil {
		t.Fatal("Parse() error = nil, want error for invalid input")
	}
}

func TestParse_Empty(t *testing.T) {
	if _, err := Parse([]byte("")); err == nil {
		t.Fatal("Parse() error = nil, want error for empty input")
	}
}
