package db

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tiim/goread/internal/model"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.sqlite")
	sqlDB, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	return sqlDB
}

func TestFeedRepo_CreateAndGet(t *testing.T) {
	sqlDB := newTestDB(t)
	repo := NewFeedRepo(sqlDB)

	now := time.Now().Truncate(time.Second)
	f := &model.Feed{
		Title:         "Example Feed",
		Description:   "An example feed",
		FeedURL:       "https://example.com/feed.xml",
		SiteURL:       "https://example.com",
		Favicon:       []byte{1, 2, 3},
		RefreshTTL:    30 * time.Minute,
		ETag:          `"abc123"`,
		LastModified:  "Wed, 21 Oct 2015 07:28:00 GMT",
		Folder:        "Tech",
		LastRefreshAt: &now,
		LastSuccessAt: &now,
		RefreshError:  "",
	}

	if err := repo.Create(f); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if f.ID == 0 {
		t.Fatal("Create() did not assign an ID")
	}

	got, err := repo.Get(f.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	if got.Title != f.Title || got.FeedURL != f.FeedURL || got.Folder != f.Folder {
		t.Errorf("Get() = %+v, want fields matching %+v", got, f)
	}
	if got.RefreshTTL != f.RefreshTTL {
		t.Errorf("RefreshTTL = %v, want %v", got.RefreshTTL, f.RefreshTTL)
	}
	if got.LastRefreshAt == nil || !got.LastRefreshAt.Equal(now) {
		t.Errorf("LastRefreshAt = %v, want %v", got.LastRefreshAt, now)
	}
	if len(got.Favicon) != 3 {
		t.Errorf("Favicon length = %d, want 3", len(got.Favicon))
	}
}

func TestFeedRepo_GetNotFound(t *testing.T) {
	sqlDB := newTestDB(t)
	repo := NewFeedRepo(sqlDB)

	if _, err := repo.Get(999); err != ErrNotFound {
		t.Errorf("Get() error = %v, want ErrNotFound", err)
	}
}

func TestFeedRepo_GetByURL(t *testing.T) {
	sqlDB := newTestDB(t)
	repo := NewFeedRepo(sqlDB)

	f := &model.Feed{Title: "Feed", FeedURL: "https://example.com/feed.xml"}
	if err := repo.Create(f); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	got, err := repo.GetByURL("https://example.com/feed.xml")
	if err != nil {
		t.Fatalf("GetByURL() error = %v", err)
	}
	if got.ID != f.ID {
		t.Errorf("GetByURL() ID = %d, want %d", got.ID, f.ID)
	}
}

func TestFeedRepo_DuplicateURLRejected(t *testing.T) {
	sqlDB := newTestDB(t)
	repo := NewFeedRepo(sqlDB)

	f1 := &model.Feed{Title: "Feed 1", FeedURL: "https://example.com/feed.xml"}
	if err := repo.Create(f1); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	f2 := &model.Feed{Title: "Feed 2", FeedURL: "https://example.com/feed.xml"}
	if err := repo.Create(f2); err == nil {
		t.Error("Create() with duplicate feed_url should have failed")
	}
}

func TestFeedRepo_Update(t *testing.T) {
	sqlDB := newTestDB(t)
	repo := NewFeedRepo(sqlDB)

	f := &model.Feed{Title: "Original", FeedURL: "https://example.com/feed.xml"}
	if err := repo.Create(f); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	f.Title = "Updated"
	f.RefreshError = "connection timed out"
	if err := repo.Update(f); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	got, err := repo.Get(f.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Title != "Updated" {
		t.Errorf("Title = %q, want %q", got.Title, "Updated")
	}
	if got.RefreshError != "connection timed out" {
		t.Errorf("RefreshError = %q, want %q", got.RefreshError, "connection timed out")
	}
}

func TestFeedRepo_UpdateNotFound(t *testing.T) {
	sqlDB := newTestDB(t)
	repo := NewFeedRepo(sqlDB)

	f := &model.Feed{ID: 999, Title: "Ghost", FeedURL: "https://example.com/ghost.xml"}
	if err := repo.Update(f); err != ErrNotFound {
		t.Errorf("Update() error = %v, want ErrNotFound", err)
	}
}

func TestFeedRepo_List(t *testing.T) {
	sqlDB := newTestDB(t)
	repo := NewFeedRepo(sqlDB)

	repo.Create(&model.Feed{Title: "B Feed", FeedURL: "https://example.com/b.xml", Folder: "Zeta"})
	repo.Create(&model.Feed{Title: "A Feed", FeedURL: "https://example.com/a.xml", Folder: "Alpha"})

	feeds, err := repo.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(feeds) != 2 {
		t.Fatalf("List() len = %d, want 2", len(feeds))
	}
	if feeds[0].Folder != "Alpha" {
		t.Errorf("List()[0].Folder = %q, want %q (ordered by folder)", feeds[0].Folder, "Alpha")
	}
}

func TestFeedRepo_DeleteCascadesArticles(t *testing.T) {
	sqlDB := newTestDB(t)
	feedRepo := NewFeedRepo(sqlDB)
	articleRepo := NewArticleRepo(sqlDB)

	f := &model.Feed{Title: "Feed", FeedURL: "https://example.com/feed.xml"}
	if err := feedRepo.Create(f); err != nil {
		t.Fatalf("Create feed error = %v", err)
	}

	a := &model.Article{FeedID: f.ID, GUID: "guid-1", Title: "Article"}
	if err := articleRepo.Create(a); err != nil {
		t.Fatalf("Create article error = %v", err)
	}

	if err := feedRepo.Delete(f.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	if _, err := articleRepo.Get(a.ID); err != ErrNotFound {
		t.Errorf("expected article to be cascade-deleted, Get() error = %v", err)
	}
}
