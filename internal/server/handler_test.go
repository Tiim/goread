package server

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Tiim/goread/internal/db"
	"github.com/Tiim/goread/internal/model"
)

func newTestHandler(t *testing.T) (*Handler, *db.FeedRepo, *db.ArticleRepo) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.sqlite")
	sqlDB, err := db.Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })

	feeds := db.NewFeedRepo(sqlDB)
	articles := db.NewArticleRepo(sqlDB)
	return NewHandler(feeds, articles), feeds, articles
}

func TestHandleIndex_NoFeeds(t *testing.T) {
	h, _, _ := newTestHandler(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "No feeds yet.") {
		t.Errorf("expected empty feed tree message, got body: %s", body)
	}
	if !strings.Contains(body, "Select a feed to see its articles.") {
		t.Errorf("expected empty article list message, got body: %s", body)
	}
	if !strings.Contains(body, "Select an article to read it.") {
		t.Errorf("expected empty article content message, got body: %s", body)
	}
}

func TestHandleFeed_ListsArticles(t *testing.T) {
	h, feeds, articles := newTestHandler(t)

	f := &model.Feed{Title: "My Feed", Folder: "Tech"}
	if err := feeds.Create(f); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	published := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	a := &model.Article{FeedID: f.ID, Title: "Hello World", PublishedAt: &published}
	if err := articles.Create(a); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/feeds/"+itoa(f.ID), nil)
	h.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "My Feed") {
		t.Errorf("expected feed title in body, got: %s", body)
	}
	if !strings.Contains(body, "Tech") {
		t.Errorf("expected folder name in body, got: %s", body)
	}
	if !strings.Contains(body, "Hello World") {
		t.Errorf("expected article title in body, got: %s", body)
	}
}

func TestHandleFeed_UnknownFeedReturns404(t *testing.T) {
	h, _, _ := newTestHandler(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/feeds/999", nil)
	h.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestHandleArticle_RendersContent(t *testing.T) {
	h, feeds, articles := newTestHandler(t)

	f := &model.Feed{Title: "My Feed"}
	if err := feeds.Create(f); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	a := &model.Article{FeedID: f.ID, Title: "Hello World", Author: "Jane Doe", ContentText: "Some plain text body."}
	if err := articles.Create(a); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/feeds/"+itoa(f.ID)+"/articles/"+itoa(a.ID), nil)
	h.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Hello World") {
		t.Errorf("expected article title in body, got: %s", body)
	}
	if !strings.Contains(body, "Jane Doe") {
		t.Errorf("expected article author in body, got: %s", body)
	}
	if !strings.Contains(body, "Some plain text body.") {
		t.Errorf("expected article content in body, got: %s", body)
	}
}

func TestHandleArticle_MismatchedFeedReturns404(t *testing.T) {
	h, feeds, articles := newTestHandler(t)

	f1 := &model.Feed{Title: "Feed One", FeedURL: "https://example.com/feed-one.xml"}
	if err := feeds.Create(f1); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	f2 := &model.Feed{Title: "Feed Two", FeedURL: "https://example.com/feed-two.xml"}
	if err := feeds.Create(f2); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	a := &model.Article{FeedID: f1.ID, Title: "Hello World"}
	if err := articles.Create(a); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/feeds/"+itoa(f2.ID)+"/articles/"+itoa(a.ID), nil)
	h.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestHandleFeed_InvalidIDReturns400(t *testing.T) {
	h, _, _ := newTestHandler(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/feeds/not-a-number", nil)
	h.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestStaticAssetsServed(t *testing.T) {
	h, _, _ := newTestHandler(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/static/style.css", nil)
	h.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "grid-template-columns") {
		t.Errorf("expected CSS content, got: %s", rec.Body.String())
	}
}

func itoa(id int64) string {
	return strconv.FormatInt(id, 10)
}
