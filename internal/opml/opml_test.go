package opml

import (
	"bytes"
	"strings"
	"testing"
)

const sampleOPML = `<?xml version="1.0" encoding="UTF-8"?>
<opml version="2.0">
  <head><title>Subscriptions</title></head>
  <body>
    <outline text="Tech">
      <outline text="Go Blog" title="Go Blog" type="rss" xmlUrl="https://go.dev/blog/feed.atom" htmlUrl="https://go.dev/blog"/>
      <outline text="Nested">
        <outline text="Deep Feed" type="rss" xmlUrl="https://deep.example.com/feed.xml"/>
      </outline>
    </outline>
    <outline text="No Folder Feed" type="rss" xmlUrl="https://top.example.com/feed.xml"/>
  </body>
</opml>
`

func TestParse(t *testing.T) {
	feeds, err := Parse(strings.NewReader(sampleOPML))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(feeds) != 3 {
		t.Fatalf("Parse() returned %d feeds, want 3: %+v", len(feeds), feeds)
	}

	want := []Feed{
		{Title: "Go Blog", FeedURL: "https://go.dev/blog/feed.atom", SiteURL: "https://go.dev/blog", Folder: "Tech"},
		{Title: "Deep Feed", FeedURL: "https://deep.example.com/feed.xml", Folder: "Tech/Nested"},
		{Title: "No Folder Feed", FeedURL: "https://top.example.com/feed.xml", Folder: ""},
	}
	for i, w := range want {
		if feeds[i] != w {
			t.Errorf("feeds[%d] = %+v, want %+v", i, feeds[i], w)
		}
	}
}

func TestParse_InvalidXML(t *testing.T) {
	_, err := Parse(strings.NewReader("not xml"))
	if err == nil {
		t.Fatal("Parse() error = nil, want error for invalid XML")
	}
}

func TestParse_Empty(t *testing.T) {
	feeds, err := Parse(strings.NewReader(`<opml version="2.0"><head></head><body></body></opml>`))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(feeds) != 0 {
		t.Errorf("Parse(empty) = %+v, want no feeds", feeds)
	}
}

func TestGenerate_RoundTrip(t *testing.T) {
	feeds := []Feed{
		{Title: "Go Blog", FeedURL: "https://go.dev/blog/feed.atom", SiteURL: "https://go.dev/blog", Folder: "Tech"},
		{Title: "Ungrouped", FeedURL: "https://top.example.com/feed.xml"},
	}

	var buf bytes.Buffer
	if err := Generate(&buf, feeds); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	got, err := Parse(&buf)
	if err != nil {
		t.Fatalf("Parse(generated) error = %v", err)
	}
	if len(got) != len(feeds) {
		t.Fatalf("round-trip returned %d feeds, want %d: %+v", len(got), len(feeds), got)
	}
	for i, f := range feeds {
		if got[i] != f {
			t.Errorf("round-trip feed[%d] = %+v, want %+v", i, got[i], f)
		}
	}
}

func TestGenerate_ContainsOPMLStructure(t *testing.T) {
	var buf bytes.Buffer
	if err := Generate(&buf, []Feed{{Title: "F", FeedURL: "https://example.com/feed.xml", Folder: "Cat"}}); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	out := buf.String()
	for _, want := range []string{"<?xml", "<opml", "xmlUrl=\"https://example.com/feed.xml\"", "Cat"} {
		if !strings.Contains(out, want) {
			t.Errorf("Generate() output missing %q; got:\n%s", want, out)
		}
	}
}

// sampleThunderbirdOPML reproduces Thunderbird's OPML export quirk of
// wrapping every single feed subscription in its own intermediate outline
// named (almost) identically to the feed - producing a redundant
// folder/podcastname/podcastname nesting that Parse must collapse back down
// to folder/podcastname.
const sampleThunderbirdOPML = `<?xml version="1.0"?>
<opml version="1.0">
  <body>
    <outline title="Comics">
      <outline title="Weekly Comic Often updated">
        <outline type="rss" title="Weekly Comic. Often updated." text="Weekly Comic. Often updated." xmlUrl="https://weeklycomic.example.com/feeds/rss/" htmlUrl="https://weeklycomic.example.com/latest/"/>
      </outline>
      <outline title="dailydoodleexample">
        <outline type="rss" title="dailydoodle.example" text="dailydoodle.example" xmlUrl="https://dailydoodle.example/rss.xml" htmlUrl="https://dailydoodle.example/"/>
      </outline>
    </outline>
    <outline title="Devs">
      <outline title="Nickname Only">
        <outline type="rss" title="Nickname Only's Extended Blog Title" text="Nickname Only's Extended Blog Title" xmlUrl="https://example-author.example/index.xml" htmlUrl="https://example-author.example/"/>
      </outline>
    </outline>
    <outline title="Solo Folder">
      <outline type="rss" title="Unrelated Feed Name" xmlUrl="https://solo.example.com/feed.xml"/>
    </outline>
  </body>
</opml>
`

func TestParse_ThunderbirdWrapperOutlinesAreCollapsed(t *testing.T) {
	feeds, err := Parse(strings.NewReader(sampleThunderbirdOPML))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	want := []Feed{
		{Title: "Weekly Comic. Often updated.", FeedURL: "https://weeklycomic.example.com/feeds/rss/", SiteURL: "https://weeklycomic.example.com/latest/", Folder: "Comics"},
		{Title: "dailydoodle.example", FeedURL: "https://dailydoodle.example/rss.xml", SiteURL: "https://dailydoodle.example/", Folder: "Comics"},
		{Title: "Nickname Only's Extended Blog Title", FeedURL: "https://example-author.example/index.xml", SiteURL: "https://example-author.example/", Folder: "Devs"},
		{Title: "Unrelated Feed Name", FeedURL: "https://solo.example.com/feed.xml", Folder: "Solo Folder"},
	}
	if len(feeds) != len(want) {
		t.Fatalf("Parse() returned %d feeds, want %d: %+v", len(feeds), len(want), feeds)
	}
	for i, w := range want {
		if feeds[i] != w {
			t.Errorf("feeds[%d] = %+v, want %+v", i, feeds[i], w)
		}
	}
}
