package db

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/Tiim/goread/internal/model"
)

func createTestFeedWithURL(t *testing.T, sqlDB *sql.DB, url, title, folder string) *model.Feed {
	t.Helper()
	f := &model.Feed{Title: title, FeedURL: url, Folder: folder}
	if err := NewFeedRepo(sqlDB).Create(f); err != nil {
		t.Fatalf("create test feed %q: %v", url, err)
	}
	return f
}

func TestMergeFeeds_ReassignsUniqueArticlesAndDeletesLoser(t *testing.T) {
	sqlDB := newTestDB(t)
	feeds := NewFeedRepo(sqlDB)
	articles := NewArticleRepo(sqlDB)

	survivor := createTestFeedWithURL(t, sqlDB, "https://a.example.com/feed.xml", "Survivor", "Tech")
	loser := createTestFeedWithURL(t, sqlDB, "https://b.example.com/feed.xml", "Loser", "Tech")

	survivorArticle := &model.Article{FeedID: survivor.ID, GUID: "s-1", Title: "Survivor Article", Link: "https://a.example.com/1"}
	if err := articles.Create(survivorArticle); err != nil {
		t.Fatalf("create survivor article: %v", err)
	}
	loserArticle := &model.Article{FeedID: loser.ID, GUID: "l-1", Title: "Loser Article", Link: "https://b.example.com/1"}
	if err := articles.Create(loserArticle); err != nil {
		t.Fatalf("create loser article: %v", err)
	}

	if err := MergeFeeds(sqlDB, survivor.ID, loser.ID); err != nil {
		t.Fatalf("MergeFeeds() error = %v", err)
	}

	if _, err := feeds.Get(loser.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("loser feed still exists after merge, err = %v", err)
	}

	got, err := articles.ListByFeed(survivor.ID)
	if err != nil {
		t.Fatalf("ListByFeed() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListByFeed() returned %d articles, want 2", len(got))
	}
	for _, a := range got {
		if a.FeedID != survivor.ID {
			t.Errorf("article %d has FeedID = %d, want %d", a.ID, a.FeedID, survivor.ID)
		}
	}
}

func TestMergeFeeds_DedupsMatchingArticlesByGUID(t *testing.T) {
	sqlDB := newTestDB(t)
	feeds := NewFeedRepo(sqlDB)
	articles := NewArticleRepo(sqlDB)

	survivor := createTestFeedWithURL(t, sqlDB, "https://a.example.com/feed.xml", "Survivor", "")
	loser := createTestFeedWithURL(t, sqlDB, "https://b.example.com/feed.xml", "Loser", "")

	survivorArticle := &model.Article{FeedID: survivor.ID, GUID: "dup-1", Title: "Same Article", Link: "https://example.com/dup", Read: false}
	if err := articles.Create(survivorArticle); err != nil {
		t.Fatalf("create survivor article: %v", err)
	}
	// Same GUID as the survivor's article: this should be recognized as the
	// same underlying article rather than duplicated.
	loserArticle := &model.Article{FeedID: loser.ID, GUID: "dup-1", Title: "Same Article", Link: "https://example.com/dup", Read: true}
	if err := articles.Create(loserArticle); err != nil {
		t.Fatalf("create loser article: %v", err)
	}

	if err := MergeFeeds(sqlDB, survivor.ID, loser.ID); err != nil {
		t.Fatalf("MergeFeeds() error = %v", err)
	}

	got, err := articles.ListByFeed(survivor.ID)
	if err != nil {
		t.Fatalf("ListByFeed() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ListByFeed() returned %d articles, want 1 (deduped)", len(got))
	}
	if !got[0].Read {
		t.Error("merged article should be marked read since the loser's copy was read")
	}
	if _, err := feeds.Get(loser.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("loser feed still exists after merge, err = %v", err)
	}
}

func TestMergeFeeds_DedupsByLinkWhenGUIDDiffers(t *testing.T) {
	sqlDB := newTestDB(t)
	articles := NewArticleRepo(sqlDB)

	survivor := createTestFeedWithURL(t, sqlDB, "https://a.example.com/feed.xml", "Survivor", "")
	loser := createTestFeedWithURL(t, sqlDB, "https://b.example.com/feed.xml", "Loser", "")

	if err := articles.Create(&model.Article{FeedID: survivor.ID, GUID: "guid-a", Link: "https://example.com/same-article", Title: "Article"}); err != nil {
		t.Fatalf("create survivor article: %v", err)
	}
	// Different GUID, but same link: should still be recognized as a
	// duplicate via the Link fallback in the identity chain.
	if err := articles.Create(&model.Article{FeedID: loser.ID, GUID: "guid-b", Link: "https://example.com/same-article", Title: "Article"}); err != nil {
		t.Fatalf("create loser article: %v", err)
	}

	if err := MergeFeeds(sqlDB, survivor.ID, loser.ID); err != nil {
		t.Fatalf("MergeFeeds() error = %v", err)
	}

	got, err := articles.ListByFeed(survivor.ID)
	if err != nil {
		t.Fatalf("ListByFeed() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ListByFeed() returned %d articles, want 1 (deduped by link)", len(got))
	}
}

func TestMergeFeeds_UpdatesSearchIndexFeedTitleForReassignedArticles(t *testing.T) {
	sqlDB := newTestDB(t)
	articles := NewArticleRepo(sqlDB)

	survivor := createTestFeedWithURL(t, sqlDB, "https://a.example.com/feed.xml", "Survivor Title", "")
	loser := createTestFeedWithURL(t, sqlDB, "https://b.example.com/feed.xml", "Loser Title", "")

	a := &model.Article{FeedID: loser.ID, GUID: "g1", Title: "Unique Findable Keyword", ContentText: "Unique Findable Keyword"}
	if err := articles.Create(a); err != nil {
		t.Fatalf("create loser article: %v", err)
	}

	if err := MergeFeeds(sqlDB, survivor.ID, loser.ID); err != nil {
		t.Fatalf("MergeFeeds() error = %v", err)
	}

	results, err := articles.Search("Unique Findable Keyword", 10)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("Search() returned %d results, want 1", len(results))
	}
	if results[0].FeedTitle != "Survivor Title" {
		t.Errorf("Search() result FeedTitle = %q, want %q", results[0].FeedTitle, "Survivor Title")
	}
}

func TestMergeFeeds_UnknownFeedReturnsErrNotFound(t *testing.T) {
	sqlDB := newTestDB(t)
	survivor := createTestFeedWithURL(t, sqlDB, "https://a.example.com/feed.xml", "Survivor", "")

	if err := MergeFeeds(sqlDB, survivor.ID, 99999); !errors.Is(err, ErrNotFound) {
		t.Errorf("MergeFeeds() error = %v, want ErrNotFound", err)
	}
	if err := MergeFeeds(sqlDB, 99999, survivor.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("MergeFeeds() error = %v, want ErrNotFound", err)
	}
}

func TestMergeFeeds_SameFeedReturnsError(t *testing.T) {
	sqlDB := newTestDB(t)
	f := createTestFeedWithURL(t, sqlDB, "https://a.example.com/feed.xml", "Feed", "")

	if err := MergeFeeds(sqlDB, f.ID, f.ID); err == nil {
		t.Error("MergeFeeds() with survivor == loser should return an error")
	}
}

func TestMergeFeeds_ClearsMergeCandidatePointers(t *testing.T) {
	sqlDB := newTestDB(t)
	feeds := NewFeedRepo(sqlDB)

	survivor := createTestFeedWithURL(t, sqlDB, "https://a.example.com/feed.xml", "Survivor", "")
	loser := createTestFeedWithURL(t, sqlDB, "https://b.example.com/feed.xml", "Loser", "")

	survivor.MergeCandidateID = &loser.ID
	if err := feeds.Update(survivor); err != nil {
		t.Fatalf("Update(survivor) error = %v", err)
	}
	loser.MergeCandidateID = &survivor.ID
	if err := feeds.Update(loser); err != nil {
		t.Fatalf("Update(loser) error = %v", err)
	}

	if err := MergeFeeds(sqlDB, survivor.ID, loser.ID); err != nil {
		t.Fatalf("MergeFeeds() error = %v", err)
	}

	got, err := feeds.Get(survivor.ID)
	if err != nil {
		t.Fatalf("Get(survivor) error = %v", err)
	}
	if got.MergeCandidateID != nil {
		t.Errorf("survivor.MergeCandidateID = %v, want nil after merge", *got.MergeCandidateID)
	}
}
