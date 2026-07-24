package feed

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tiim/goread/internal/db"
	"github.com/Tiim/goread/internal/model"
)

func newTestRefresher(t *testing.T) (*Refresher, *db.FeedRepo, *db.ArticleRepo) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.sqlite")
	sqlDB, err := db.Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })

	feeds := db.NewFeedRepo(sqlDB)
	articles := db.NewArticleRepo(sqlDB)
	return NewRefresher(feeds, articles), feeds, articles
}

const refreshFeedXML = `<?xml version="1.0"?>
<rss version="2.0">
  <channel>
    <title>Feed Title</title>
    <link>https://example.com</link>
    <description>Feed Description</description>
    <ttl>30</ttl>
    <item>
      <title>Item One</title>
      <link>https://example.com/one</link>
      <guid>guid-1</guid>
      <description>One summary</description>
      <pubDate>Mon, 02 Jan 2006 15:04:05 +0000</pubDate>
    </item>
  </channel>
</rss>`

func TestRefresher_Refresh_FetchesParsesAndSyncs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"v1"`)
		w.Write([]byte(refreshFeedXML))
	}))
	defer srv.Close()

	refresher, feeds, articles := newTestRefresher(t)
	f := &model.Feed{FeedURL: srv.URL}
	if err := feeds.Create(f); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	result, err := refresher.Refresh(context.Background(), f.ID)
	if err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	if result.Created != 1 {
		t.Errorf("Created = %d, want 1", result.Created)
	}

	got, err := feeds.Get(f.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Title != "Feed Title" {
		t.Errorf("Title = %q", got.Title)
	}
	if got.ETag != `"v1"` {
		t.Errorf("ETag = %q, want %q", got.ETag, `"v1"`)
	}
	if got.RefreshTTL != 30*time.Minute {
		t.Errorf("RefreshTTL = %v, want 30m", got.RefreshTTL)
	}
	if got.LastRefreshAt == nil || got.LastSuccessAt == nil {
		t.Fatal("expected LastRefreshAt and LastSuccessAt to be set")
	}
	if got.RefreshError != "" {
		t.Errorf("RefreshError = %q, want empty", got.RefreshError)
	}

	articlesGot, err := articles.ListByFeed(f.ID)
	if err != nil {
		t.Fatalf("ListByFeed() error = %v", err)
	}
	if len(articlesGot) != 1 {
		t.Fatalf("len(articles) = %d, want 1", len(articlesGot))
	}
}

func TestRefresher_Refresh_NotModifiedLeavesArticlesUnchanged(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("If-None-Match") == `"v1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		w.Write([]byte(refreshFeedXML))
	}))
	defer srv.Close()

	refresher, feeds, articles := newTestRefresher(t)
	f := &model.Feed{FeedURL: srv.URL}
	if err := feeds.Create(f); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if _, err := refresher.Refresh(context.Background(), f.ID); err != nil {
		t.Fatalf("Refresh() #1 error = %v", err)
	}
	result, err := refresher.Refresh(context.Background(), f.ID)
	if err != nil {
		t.Fatalf("Refresh() #2 error = %v", err)
	}
	if result.Created != 0 || result.Updated != 0 {
		t.Errorf("result = %+v, want no changes on 304", result)
	}
	if calls != 2 {
		t.Fatalf("server received %d requests, want 2", calls)
	}

	got, err := articles.ListByFeed(f.ID)
	if err != nil {
		t.Fatalf("ListByFeed() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(articles) = %d, want 1 (unchanged)", len(got))
	}
}

func TestRefresher_Refresh_FetchErrorRecordsErrorAndKeepsData(t *testing.T) {
	up := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !up {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Write([]byte(refreshFeedXML))
	}))
	defer srv.Close()

	refresher, feeds, articles := newTestRefresher(t)
	f := &model.Feed{FeedURL: srv.URL}
	if err := feeds.Create(f); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if _, err := refresher.Refresh(context.Background(), f.ID); err != nil {
		t.Fatalf("Refresh() #1 error = %v", err)
	}

	up = false
	if _, err := refresher.Refresh(context.Background(), f.ID); err != nil {
		t.Fatalf("Refresh() #2 error = %v, want nil (errors are recorded, not returned)", err)
	}

	got, err := feeds.Get(f.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.RefreshError == "" {
		t.Error("RefreshError = empty, want a recorded error")
	}
	if got.Title != "Feed Title" {
		t.Errorf("Title = %q, want unchanged existing data", got.Title)
	}

	articlesGot, err := articles.ListByFeed(f.ID)
	if err != nil {
		t.Fatalf("ListByFeed() error = %v", err)
	}
	if len(articlesGot) != 1 {
		t.Fatalf("len(articles) = %d, want 1 (unchanged after failed refresh)", len(articlesGot))
	}
}

func TestRefresher_Refresh_PermanentRedirectPersistsNewURL(t *testing.T) {
	var newURL string
	mux := http.NewServeMux()
	mux.HandleFunc("/old.xml", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, newURL, http.StatusMovedPermanently)
	})
	mux.HandleFunc("/new.xml", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(refreshFeedXML))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	newURL = srv.URL + "/new.xml"

	refresher, feeds, _ := newTestRefresher(t)
	f := &model.Feed{FeedURL: srv.URL + "/old.xml"}
	if err := feeds.Create(f); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if _, err := refresher.Refresh(context.Background(), f.ID); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	got, err := feeds.Get(f.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.FeedURL != newURL {
		t.Errorf("FeedURL = %q, want %q", got.FeedURL, newURL)
	}
}

func TestDue(t *testing.T) {
	now := time.Now()

	t.Run("never refreshed is due", func(t *testing.T) {
		f := &model.Feed{}
		if !Due(f, now) {
			t.Error("Due() = false, want true for never-refreshed feed")
		}
	})

	t.Run("within TTL is not due", func(t *testing.T) {
		last := now.Add(-5 * time.Minute)
		f := &model.Feed{LastRefreshAt: &last, RefreshTTL: 30 * time.Minute}
		if Due(f, now) {
			t.Error("Due() = true, want false within TTL")
		}
	})

	t.Run("past TTL is due", func(t *testing.T) {
		last := now.Add(-31 * time.Minute)
		f := &model.Feed{LastRefreshAt: &last, RefreshTTL: 30 * time.Minute}
		if !Due(f, now) {
			t.Error("Due() = false, want true past TTL")
		}
	})

	t.Run("zero TTL falls back to DefaultTTL", func(t *testing.T) {
		last := now.Add(-1 * time.Minute)
		f := &model.Feed{LastRefreshAt: &last, RefreshTTL: 0}
		if Due(f, now) {
			t.Error("Due() = true, want false (DefaultTTL not yet elapsed)")
		}
	})
}
