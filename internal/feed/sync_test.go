package feed

import (
	"path/filepath"
	"testing"

	"github.com/Tiim/goread/internal/db"
	"github.com/Tiim/goread/internal/model"
)

func newTestSyncer(t *testing.T) (*Syncer, *db.FeedRepo, *db.ArticleRepo) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.sqlite")
	sqlDB, err := db.Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })

	feeds := db.NewFeedRepo(sqlDB)
	articles := db.NewArticleRepo(sqlDB)
	return NewSyncer(feeds, articles), feeds, articles
}

const twoItemFeed = `<?xml version="1.0"?>
<rss version="2.0">
  <channel>
    <title>Feed Title</title>
    <link>https://example.com</link>
    <description>Feed Description</description>
    <item>
      <title>Item One</title>
      <link>https://example.com/one</link>
      <guid>guid-1</guid>
      <description>One summary</description>
      <pubDate>Mon, 02 Jan 2006 15:04:05 +0000</pubDate>
    </item>
    <item>
      <title>Item Two</title>
      <link>https://example.com/two</link>
      <guid>guid-2</guid>
      <description>Two summary</description>
      <pubDate>Tue, 03 Jan 2006 15:04:05 +0000</pubDate>
    </item>
  </channel>
</rss>`

const oneItemFeed = `<?xml version="1.0"?>
<rss version="2.0">
  <channel>
    <title>Feed Title Updated</title>
    <link>https://example.com</link>
    <description>Feed Description</description>
    <item>
      <title>Item One Updated</title>
      <link>https://example.com/one</link>
      <guid>guid-1</guid>
      <description>One summary updated</description>
      <pubDate>Mon, 02 Jan 2006 15:04:05 +0000</pubDate>
    </item>
  </channel>
</rss>`

func mustCreateFeed(t *testing.T, repo *db.FeedRepo) *model.Feed {
	t.Helper()
	f := &model.Feed{FeedURL: "https://example.com/feed.xml"}
	if err := repo.Create(f); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	return f
}

func TestSyncer_Sync_InsertsNewArticles(t *testing.T) {
	syncer, feeds, articles := newTestSyncer(t)
	f := mustCreateFeed(t, feeds)

	parsed, err := Parse([]byte(twoItemFeed))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	result, err := syncer.Sync(f.ID, parsed)
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if result.Created != 2 || result.Updated != 0 || result.Deleted != 0 {
		t.Errorf("result = %+v, want {Created: 2}", result)
	}

	got, err := articles.ListByFeed(f.ID)
	if err != nil {
		t.Fatalf("ListByFeed() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(articles) = %d, want 2", len(got))
	}

	updatedFeed, err := feeds.Get(f.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if updatedFeed.Title != "Feed Title" {
		t.Errorf("feed Title = %q, want %q", updatedFeed.Title, "Feed Title")
	}
}

func TestSyncer_Sync_UpdatesExistingAndPreservesReadState(t *testing.T) {
	syncer, feeds, articles := newTestSyncer(t)
	f := mustCreateFeed(t, feeds)

	parsed, err := Parse([]byte(twoItemFeed))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if _, err := syncer.Sync(f.ID, parsed); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}

	all, err := articles.ListByFeed(f.ID)
	if err != nil {
		t.Fatalf("ListByFeed() error = %v", err)
	}
	var firstID int64
	for _, a := range all {
		if a.GUID == "guid-1" {
			firstID = a.ID
			if err := articles.SetRead(a.ID, true); err != nil {
				t.Fatalf("SetRead() error = %v", err)
			}
		}
	}
	if firstID == 0 {
		t.Fatalf("could not find article with guid-1")
	}

	// Re-sync with the same feed content: title changed, guid-1 unchanged content.
	result, err := syncer.Sync(f.ID, parsed)
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if result.Created != 0 || result.Updated != 2 {
		t.Errorf("result = %+v, want {Updated: 2}", result)
	}

	got, err := articles.Get(firstID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !got.Read {
		t.Errorf("Read = false, want true (read state should survive re-sync)")
	}
	if got.ID != firstID {
		t.Errorf("ID changed across sync: %d != %d", got.ID, firstID)
	}
}

func TestSyncer_Sync_MarksMissingArticlesDeleted(t *testing.T) {
	syncer, feeds, articles := newTestSyncer(t)
	f := mustCreateFeed(t, feeds)

	parsed, err := Parse([]byte(twoItemFeed))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if _, err := syncer.Sync(f.ID, parsed); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}

	// Second sync only contains guid-1; guid-2 should be marked deleted, not removed.
	parsedOne, err := Parse([]byte(oneItemFeed))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	result, err := syncer.Sync(f.ID, parsedOne)
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if result.Deleted != 1 {
		t.Errorf("Deleted = %d, want 1", result.Deleted)
	}

	got, err := articles.ListByFeed(f.ID)
	if err != nil {
		t.Fatalf("ListByFeed() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(articles) = %d, want 2 (missing article must remain in DB)", len(got))
	}
	for _, a := range got {
		if a.GUID == "guid-2" && a.State != model.ArticleStateDeleted {
			t.Errorf("guid-2 State = %q, want %q", a.State, model.ArticleStateDeleted)
		}
		if a.GUID == "guid-1" && a.State != model.ArticleStatePresent {
			t.Errorf("guid-1 State = %q, want %q", a.State, model.ArticleStatePresent)
		}
	}
}

func TestSyncer_Sync_ResurrectsReappearingArticle(t *testing.T) {
	syncer, feeds, articles := newTestSyncer(t)
	f := mustCreateFeed(t, feeds)

	full, err := Parse([]byte(twoItemFeed))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	partial, err := Parse([]byte(oneItemFeed))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if _, err := syncer.Sync(f.ID, full); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if _, err := syncer.Sync(f.ID, partial); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	// guid-2 disappears, then reappears.
	if _, err := syncer.Sync(f.ID, full); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}

	got, err := articles.ListByFeed(f.ID)
	if err != nil {
		t.Fatalf("ListByFeed() error = %v", err)
	}
	for _, a := range got {
		if a.GUID == "guid-2" && a.State != model.ArticleStatePresent {
			t.Errorf("guid-2 State = %q, want %q after reappearing", a.State, model.ArticleStatePresent)
		}
	}
}
