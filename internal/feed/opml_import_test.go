package feed

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tiim/goread/internal/db"
	"github.com/Tiim/goread/internal/model"
)

const sampleImportOPML = `<?xml version="1.0"?>
<opml version="2.0">
  <head><title>Subs</title></head>
  <body>
    <outline text="Tech">
      <outline text="New Feed" type="rss" xmlUrl="https://new.example.com/feed.xml"/>
      <outline text="Renamed" type="rss" xmlUrl="https://existing.example.com/feed.xml" htmlUrl="https://existing.example.com"/>
    </outline>
  </body>
</opml>
`

func TestImportOPML_CreatesAndUpdatesFeeds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.sqlite")
	sqlDB, err := db.Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer sqlDB.Close()

	feeds := db.NewFeedRepo(sqlDB)
	existing := &model.Feed{Title: "Old Title", FeedURL: "https://existing.example.com/feed.xml", Folder: "Uncategorized"}
	if err := feeds.Create(existing); err != nil {
		t.Fatalf("create existing feed: %v", err)
	}

	result, err := ImportOPML(feeds, nil, strings.NewReader(sampleImportOPML))
	if err != nil {
		t.Fatalf("ImportOPML() error = %v", err)
	}
	if result.Created != 1 || result.Updated != 1 {
		t.Fatalf("ImportOPML() result = %+v, want Created=1 Updated=1", result)
	}

	all, err := feeds.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("List() returned %d feeds, want 2", len(all))
	}

	updated, err := feeds.Get(existing.ID)
	if err != nil {
		t.Fatalf("Get(existing) error = %v", err)
	}
	if updated.Title != "Renamed" || updated.Folder != "Tech" || updated.SiteURL != "https://existing.example.com" {
		t.Errorf("existing feed after import = %+v, want Title=Renamed Folder=Tech SiteURL=https://existing.example.com", updated)
	}

	created, err := feeds.GetByURL("https://new.example.com/feed.xml")
	if err != nil {
		t.Fatalf("GetByURL(new) error = %v", err)
	}
	if created.Title != "New Feed" || created.Folder != "Tech" {
		t.Errorf("new feed = %+v, want Title=New Feed Folder=Tech", created)
	}
}

func TestImportOPML_TriggersRefresh(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.sqlite")
	sqlDB, err := db.Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer sqlDB.Close()

	feeds := db.NewFeedRepo(sqlDB)
	articles := db.NewArticleRepo(sqlDB)
	refresher := NewRefresher(feeds, articles)
	refresher.Favicon = nil // keep tests offline
	sched := NewScheduler(feeds, refresher, time.Hour)

	if _, err := ImportOPML(feeds, sched, strings.NewReader(sampleImportOPML)); err != nil {
		t.Fatalf("ImportOPML() error = %v", err)
	}

	select {
	case req := <-sched.manual:
		if req.feedID == 0 {
			t.Errorf("queued refresh has zero feedID")
		}
	default:
		t.Fatal("ImportOPML() did not queue a refresh on the scheduler")
	}
}

func TestImportOPML_InvalidXML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.sqlite")
	sqlDB, err := db.Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer sqlDB.Close()

	feeds := db.NewFeedRepo(sqlDB)
	if _, err := ImportOPML(feeds, nil, strings.NewReader("not opml")); err == nil {
		t.Fatal("ImportOPML() error = nil, want error for invalid XML")
	}
}
