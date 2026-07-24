package db

import (
	"database/sql"
	"testing"
	"time"

	"github.com/Tiim/goread/internal/model"
)

func createTestFeed(t *testing.T, sqlDB *sql.DB) *model.Feed {
	t.Helper()
	f := &model.Feed{Title: "Feed", FeedURL: "https://example.com/feed.xml"}
	if err := NewFeedRepo(sqlDB).Create(f); err != nil {
		t.Fatalf("create test feed: %v", err)
	}
	return f
}

func TestArticleRepo_CreateAndGet(t *testing.T) {
	sqlDB := newTestDB(t)
	feed := createTestFeed(t, sqlDB)
	repo := NewArticleRepo(sqlDB)

	now := time.Now().Truncate(time.Second)
	a := &model.Article{
		FeedID:      feed.ID,
		GUID:        "guid-1",
		Title:       "Hello World",
		Author:      "Jane Doe",
		PublishedAt: &now,
		UpdatedAt:   &now,
		Link:        "https://example.com/article",
		Summary:     "A summary",
		Content:     "<p>Full content</p>",
		ContentText: "Full content",
		ContentType: "html",
		Read:        false,
		ContentHash: "hash123",
		Metadata:    `{"custom":"value"}`,
	}

	if err := repo.Create(a); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if a.ID == 0 {
		t.Fatal("Create() did not assign an ID")
	}

	got, err := repo.Get(a.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Title != a.Title || got.GUID != a.GUID || got.FeedID != feed.ID {
		t.Errorf("Get() = %+v, want fields matching %+v", got, a)
	}
	if got.State != model.ArticleStatePresent {
		t.Errorf("State = %q, want %q (default)", got.State, model.ArticleStatePresent)
	}
	if got.PublishedAt == nil || !got.PublishedAt.Equal(now) {
		t.Errorf("PublishedAt = %v, want %v", got.PublishedAt, now)
	}
}

func TestArticleRepo_ForeignKeyConstraint(t *testing.T) {
	sqlDB := newTestDB(t)
	repo := NewArticleRepo(sqlDB)

	a := &model.Article{FeedID: 999, GUID: "guid-1", Title: "Orphan"}
	if err := repo.Create(a); err == nil {
		t.Error("Create() with nonexistent feed_id should have failed due to foreign key constraint")
	}
}

func TestArticleRepo_Update(t *testing.T) {
	sqlDB := newTestDB(t)
	feed := createTestFeed(t, sqlDB)
	repo := NewArticleRepo(sqlDB)

	a := &model.Article{FeedID: feed.ID, GUID: "guid-1", Title: "Original"}
	if err := repo.Create(a); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	a.Title = "Updated"
	a.Read = true
	if err := repo.Update(a); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	got, err := repo.Get(a.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Title != "Updated" || !got.Read {
		t.Errorf("Get() = %+v, want Title=Updated Read=true", got)
	}
}

func TestArticleRepo_SetRead(t *testing.T) {
	sqlDB := newTestDB(t)
	feed := createTestFeed(t, sqlDB)
	repo := NewArticleRepo(sqlDB)

	a := &model.Article{FeedID: feed.ID, GUID: "guid-1", Title: "Article"}
	repo.Create(a)

	if err := repo.SetRead(a.ID, true); err != nil {
		t.Fatalf("SetRead() error = %v", err)
	}
	got, _ := repo.Get(a.ID)
	if !got.Read {
		t.Error("expected article to be marked read")
	}

	if err := repo.SetRead(a.ID, false); err != nil {
		t.Fatalf("SetRead() error = %v", err)
	}
	got, _ = repo.Get(a.ID)
	if got.Read {
		t.Error("expected article to be marked unread")
	}
}

func TestArticleRepo_MarkAllReadForFeed(t *testing.T) {
	sqlDB := newTestDB(t)
	feed := createTestFeed(t, sqlDB)
	repo := NewArticleRepo(sqlDB)

	a1 := &model.Article{FeedID: feed.ID, GUID: "guid-1", Title: "One"}
	a2 := &model.Article{FeedID: feed.ID, GUID: "guid-2", Title: "Two"}
	repo.Create(a1)
	repo.Create(a2)

	if err := repo.MarkAllReadForFeed(feed.ID); err != nil {
		t.Fatalf("MarkAllReadForFeed() error = %v", err)
	}

	articles, err := repo.ListByFeed(feed.ID)
	if err != nil {
		t.Fatalf("ListByFeed() error = %v", err)
	}
	for _, a := range articles {
		if !a.Read {
			t.Errorf("article %d not marked read", a.ID)
		}
	}
}

func TestArticleRepo_FindByIdentity(t *testing.T) {
	sqlDB := newTestDB(t)
	feed := createTestFeed(t, sqlDB)
	repo := NewArticleRepo(sqlDB)

	byGUID := &model.Article{FeedID: feed.ID, GUID: "guid-1", Link: "https://example.com/1", ContentHash: "hash-1", Title: "By GUID"}
	byLink := &model.Article{FeedID: feed.ID, GUID: "", Link: "https://example.com/2", ContentHash: "hash-2", Title: "By Link"}
	byHash := &model.Article{FeedID: feed.ID, GUID: "", Link: "", ContentHash: "hash-3", Title: "By Hash"}
	repo.Create(byGUID)
	repo.Create(byLink)
	repo.Create(byHash)

	tests := []struct {
		name                    string
		guid, link, contentHash string
		wantID                  int64
	}{
		{"match by guid", "guid-1", "https://example.com/1", "hash-1", byGUID.ID},
		{"match by link when no guid", "", "https://example.com/2", "hash-2", byLink.ID},
		{"match by content hash when no guid or link", "", "", "hash-3", byHash.ID},
		{"guid takes priority over link", "guid-1", "https://example.com/2", "", byGUID.ID},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := repo.FindByIdentity(feed.ID, tt.guid, tt.link, tt.contentHash)
			if err != nil {
				t.Fatalf("FindByIdentity() error = %v", err)
			}
			if got.ID != tt.wantID {
				t.Errorf("FindByIdentity() ID = %d, want %d", got.ID, tt.wantID)
			}
		})
	}
}

func TestArticleRepo_FindByIdentityNotFound(t *testing.T) {
	sqlDB := newTestDB(t)
	feed := createTestFeed(t, sqlDB)
	repo := NewArticleRepo(sqlDB)

	_, err := repo.FindByIdentity(feed.ID, "nonexistent", "https://example.com/none", "no-hash")
	if err != ErrNotFound {
		t.Errorf("FindByIdentity() error = %v, want ErrNotFound", err)
	}
}

func TestArticleRepo_Search(t *testing.T) {
	sqlDB := newTestDB(t)
	feeds := NewFeedRepo(sqlDB)
	repo := NewArticleRepo(sqlDB)

	feedA := &model.Feed{Title: "Golang Weekly", FeedURL: "https://a.example.com/feed.xml"}
	if err := feeds.Create(feedA); err != nil {
		t.Fatalf("create feed a: %v", err)
	}
	feedB := &model.Feed{Title: "Cooking Times", FeedURL: "https://b.example.com/feed.xml"}
	if err := feeds.Create(feedB); err != nil {
		t.Fatalf("create feed b: %v", err)
	}

	articles := []*model.Article{
		{FeedID: feedA.ID, Title: "Understanding Goroutines", Author: "Ada", ContentText: "Concurrency in Go explained"},
		{FeedID: feedA.ID, Title: "Pasta Recipes", Author: "Bob", ContentText: "Nothing about programming"},
		{FeedID: feedB.ID, Title: "Weekly Roundup", Author: "Cleo", ContentText: "Mentions Golang tooling in passing"},
	}
	for _, a := range articles {
		if err := repo.Create(a); err != nil {
			t.Fatalf("create article %q: %v", a.Title, err)
		}
	}

	t.Run("matches title and content text", func(t *testing.T) {
		results, err := repo.Search("goroutine", 10)
		if err != nil {
			t.Fatalf("Search() error = %v", err)
		}
		if len(results) != 1 || results[0].Article.Title != "Understanding Goroutines" {
			t.Fatalf("Search(goroutine) = %+v, want single match on Understanding Goroutines", results)
		}
	})

	t.Run("matches feed name", func(t *testing.T) {
		results, err := repo.Search("Cooking", 10)
		if err != nil {
			t.Fatalf("Search() error = %v", err)
		}
		if len(results) != 1 || results[0].Article.Title != "Weekly Roundup" {
			t.Fatalf("Search(Cooking) = %+v, want single match on Weekly Roundup (via feed name)", results)
		}
		if results[0].FeedTitle != "Cooking Times" {
			t.Errorf("FeedTitle = %q, want %q", results[0].FeedTitle, "Cooking Times")
		}
	})

	t.Run("prefix matching", func(t *testing.T) {
		results, err := repo.Search("Gol", 10)
		if err != nil {
			t.Fatalf("Search() error = %v", err)
		}
		if len(results) != 3 {
			t.Fatalf("Search(Gol) = %d results, want 3 (2 via the \"Golang Weekly\" feed name, 1 via content text mentioning Golang)", len(results))
		}
	})

	t.Run("empty query returns no results", func(t *testing.T) {
		results, err := repo.Search("   ", 10)
		if err != nil {
			t.Fatalf("Search() error = %v", err)
		}
		if results != nil {
			t.Errorf("Search(empty) = %v, want nil", results)
		}
	})

	t.Run("special characters are escaped, not treated as FTS syntax", func(t *testing.T) {
		if _, err := repo.Search(`"unterminated OR *`, 10); err != nil {
			t.Errorf("Search() with FTS-special input error = %v, want no error", err)
		}
	})

	t.Run("feed rename updates feed_title index", func(t *testing.T) {
		feedA.Title = "Rustlang Weekly"
		if err := feeds.Update(feedA); err != nil {
			t.Fatalf("rename feed: %v", err)
		}
		results, err := repo.Search("Rustlang", 10)
		if err != nil {
			t.Fatalf("Search() error = %v", err)
		}
		if len(results) != 2 {
			t.Fatalf("Search(Rustlang) after rename = %d results, want 2", len(results))
		}
	})
}
