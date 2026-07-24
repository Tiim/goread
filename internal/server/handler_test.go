package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Tiim/goread/internal/db"
	"github.com/Tiim/goread/internal/imageproxy"
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
	wantSrc := "/feeds/" + itoa(f.ID) + "/articles/" + itoa(a.ID) + "/content"
	if !strings.Contains(body, wantSrc) {
		t.Errorf("expected sandboxed iframe pointing at %q, got: %s", wantSrc, body)
	}
	if !strings.Contains(body, `sandbox=""`) {
		t.Errorf("expected empty sandbox attribute on article iframe, got: %s", body)
	}
}

func TestHandleArticleFrame_SanitizesContentAndProxiesImages(t *testing.T) {
	h, feeds, articles := newTestHandler(t)

	f := &model.Feed{Title: "My Feed"}
	if err := feeds.Create(f); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	raw := `<p onclick="evil()">Hello <b>World</b></p><script>alert(1)</script>` +
		`<img src="https://example.com/cat.png" onerror="evil()">`
	a := &model.Article{FeedID: f.ID, Title: "Hello World", Content: raw}
	if err := articles.Create(a); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/feeds/"+itoa(f.ID)+"/articles/"+itoa(a.ID)+"/content", nil)
	h.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "<script") {
		t.Errorf("expected <script> to be stripped, got: %s", body)
	}
	if strings.Contains(body, "onclick") || strings.Contains(body, "onerror") {
		t.Errorf("expected inline event handlers to be stripped, got: %s", body)
	}
	if !strings.Contains(body, "Hello <b>World</b>") {
		t.Errorf("expected safe formatting to be preserved, got: %s", body)
	}
	if strings.Contains(body, `src="https://example.com/cat.png"`) {
		t.Errorf("expected image src to be proxied, got: %s", body)
	}
	if !strings.Contains(body, `/proxy/image?url=`) {
		t.Errorf("expected image src rewritten to local proxy, got: %s", body)
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") {
		t.Errorf("expected strict frame CSP, got: %q", csp)
	}
}

func TestHandleArticleFrame_MismatchedFeedReturns404(t *testing.T) {
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
	req := httptest.NewRequest(http.MethodGet, "/feeds/"+itoa(f2.ID)+"/articles/"+itoa(a.ID)+"/content", nil)
	h.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestPageResponses_HaveStrictCSP(t *testing.T) {
	h, _, _ := newTestHandler(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h.Routes().ServeHTTP(rec, req)

	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "script-src 'none'") {
		t.Errorf("expected script-src 'none' in page CSP, got: %q", csp)
	}
}

func TestHandleImageProxy_RejectsPrivateAddresses(t *testing.T) {
	h, _, _ := newTestHandler(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/proxy/image?url="+url.QueryEscape("http://127.0.0.1/secret.png"), nil)
	h.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
}

func TestHandleImageProxy_ServesRemoteImage(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte("fake-png-bytes"))
	}))
	defer upstream.Close()

	h, _, _ := newTestHandler(t)
	h.images = imageproxy.NewForTesting()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/proxy/image?url="+url.QueryEscape(upstream.URL+"/cat.png"), nil)
	h.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("Content-Type = %q, want image/png", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	if rec.Body.String() != "fake-png-bytes" {
		t.Errorf("body = %q, want fake-png-bytes", rec.Body.String())
	}
}

func TestHandleImageProxy_RejectsNonImageResponses(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html>not an image</html>"))
	}))
	defer upstream.Close()

	h, _, _ := newTestHandler(t)
	h.images = imageproxy.NewForTesting()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/proxy/image?url="+url.QueryEscape(upstream.URL), nil)
	h.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
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
