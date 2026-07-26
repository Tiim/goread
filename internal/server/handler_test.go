package server

import (
	"bytes"
	"errors"
	"mime/multipart"
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
	return NewHandler(feeds, articles, nil, sqlDB), feeds, articles
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
	if !strings.Contains(body, `sandbox="allow-popups allow-popups-to-escape-sandbox"`) {
		t.Errorf("expected sandbox attribute allowing popups on article iframe, got: %s", body)
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
	if !strings.Contains(csp, "script-src 'self'") {
		t.Errorf("expected script-src 'self' in page CSP, got: %q", csp)
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

func TestHandleArticle_MarksReadOnView(t *testing.T) {
	h, feeds, articles := newTestHandler(t)

	f := &model.Feed{Title: "My Feed"}
	if err := feeds.Create(f); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	a := &model.Article{FeedID: f.ID, Title: "Hello World"}
	if err := articles.Create(a); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/feeds/"+itoa(f.ID)+"/articles/"+itoa(a.ID), nil)
	h.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	got, err := articles.Get(a.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !got.Read {
		t.Errorf("expected article to be marked read after viewing")
	}
}

func TestHandleSetArticleRead_TogglesState(t *testing.T) {
	h, feeds, articles := newTestHandler(t)

	f := &model.Feed{Title: "My Feed"}
	if err := feeds.Create(f); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	a := &model.Article{FeedID: f.ID, Title: "Hello World", Read: true}
	if err := articles.Create(a); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/feeds/"+itoa(f.ID)+"/articles/"+itoa(a.ID)+"/read",
		strings.NewReader("read=false"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	h.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
	got, err := articles.Get(a.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Read {
		t.Errorf("expected article to be marked unread")
	}
	if !strings.Contains(rec.Body.String(), "Mark read") {
		t.Errorf("expected re-rendered fragment to offer 'Mark read', got: %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "<!DOCTYPE") {
		t.Errorf("expected HX-Request to receive a fragment without doctype, got: %s", rec.Body.String())
	}
}

func TestHandleMarkAllRead_MarksEveryArticle(t *testing.T) {
	h, feeds, articles := newTestHandler(t)

	f := &model.Feed{Title: "My Feed"}
	if err := feeds.Create(f); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	a1 := &model.Article{FeedID: f.ID, Title: "One"}
	a2 := &model.Article{FeedID: f.ID, Title: "Two"}
	if err := articles.Create(a1); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := articles.Create(a2); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/feeds/"+itoa(f.ID)+"/mark-all-read", nil)
	h.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
	for _, id := range []int64{a1.ID, a2.ID} {
		got, err := articles.Get(id)
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if !got.Read {
			t.Errorf("expected article %d to be marked read", id)
		}
	}
}

func TestHandleRefreshFeed_NilSchedulerStillRenders(t *testing.T) {
	h, feeds, _ := newTestHandler(t)

	f := &model.Feed{Title: "My Feed"}
	if err := feeds.Create(f); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/feeds/"+itoa(f.ID)+"/refresh", nil)
	h.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "My Feed") {
		t.Errorf("expected re-rendered feed page, got: %s", rec.Body.String())
	}
}

func TestHandleRefreshFeed_UnknownFeedReturns404(t *testing.T) {
	h, _, _ := newTestHandler(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/feeds/999/refresh", nil)
	h.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func itoa(id int64) string {
	return strconv.FormatInt(id, 10)
}

func TestHandleSearch_FindsMatchingArticles(t *testing.T) {
	h, feeds, articles := newTestHandler(t)

	f := &model.Feed{Title: "My Feed"}
	if err := feeds.Create(f); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	a := &model.Article{FeedID: f.ID, Title: "Unique Searchable Title", ContentText: "body"}
	if err := articles.Create(a); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/search?q=Searchable", nil)
	h.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Unique Searchable Title") {
		t.Errorf("expected matching article in results, got: %s", rec.Body.String())
	}
}

func TestHandleSearch_EmptyQueryShowsNoResultsBlock(t *testing.T) {
	h, _, _ := newTestHandler(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/search?q=", nil)
	h.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "No matching articles.") {
		t.Errorf("expected empty search results message, got: %s", rec.Body.String())
	}
}

func TestHandleFeedFavicon_ServesStoredBlob(t *testing.T) {
	h, feeds, _ := newTestHandler(t)

	f := &model.Feed{Title: "My Feed", Favicon: []byte{1, 2, 3, 4}, FaviconContentType: "image/png"}
	if err := feeds.Create(f); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/feeds/"+itoa(f.ID)+"/favicon", nil)
	h.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("Content-Type = %q, want image/png", ct)
	}
	if !bytes.Equal(rec.Body.Bytes(), []byte{1, 2, 3, 4}) {
		t.Errorf("body = %v, want favicon bytes", rec.Body.Bytes())
	}
}

func TestHandleFeedFavicon_NoFaviconReturns404(t *testing.T) {
	h, feeds, _ := newTestHandler(t)

	f := &model.Feed{Title: "My Feed"}
	if err := feeds.Create(f); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/feeds/"+itoa(f.ID)+"/favicon", nil)
	h.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestHandleOPMLExport_ListsFeeds(t *testing.T) {
	h, feeds, _ := newTestHandler(t)

	f := &model.Feed{Title: "My Feed", FeedURL: "https://example.com/feed.xml", Folder: "Tech"}
	if err := feeds.Create(f); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/opml/export", nil)
	h.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "https://example.com/feed.xml") || !strings.Contains(body, "Tech") {
		t.Errorf("expected exported feed URL and folder in OPML, got: %s", body)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Errorf("Content-Disposition = %q, want attachment", cd)
	}
}

func TestHandleBackup_ServesSQLiteFile(t *testing.T) {
	h, feeds, _ := newTestHandler(t)

	f := &model.Feed{Title: "My Feed", FeedURL: "https://example.com/feed.xml"}
	if err := feeds.Create(f); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/backup", nil)
	h.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Errorf("Content-Disposition = %q, want attachment", cd)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("SQLite format 3")) {
		t.Errorf("backup body does not look like a SQLite file")
	}
}

func TestHandleOPMLImport_CreatesFeedFromUpload(t *testing.T) {
	h, feeds, _ := newTestHandler(t)

	const doc = `<?xml version="1.0"?>
<opml version="2.0"><head><title>t</title></head><body>
<outline text="New" type="rss" xmlUrl="https://imported.example.com/feed.xml"/>
</body></opml>`

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("file", "subs.opml")
	if err != nil {
		t.Fatalf("CreateFormFile() error = %v", err)
	}
	if _, err := part.Write([]byte(doc)); err != nil {
		t.Fatalf("write part: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/opml/import", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	h.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}

	got, err := feeds.GetByURL("https://imported.example.com/feed.xml")
	if err != nil {
		t.Fatalf("GetByURL() error = %v", err)
	}
	if got.Title != "New" {
		t.Errorf("Title = %q, want New", got.Title)
	}
}

func TestHandleOPMLImport_MissingFileReturns400(t *testing.T) {
	h, _, _ := newTestHandler(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/opml/import", strings.NewReader(""))
	h.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func postForm(h *Handler, path string, form url.Values) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.Routes().ServeHTTP(rec, req)
	return rec
}

func TestHandleAddFeed_CreatesFeedAndRedirectsToIt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<?xml version="1.0"?><rss version="2.0"><channel><title>Added</title></channel></rss>`))
	}))
	defer srv.Close()

	h, feeds, _ := newTestHandler(t)

	rec := postForm(h, "/feeds", url.Values{"url": {srv.URL}, "folder": {"Tech"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}

	got, err := feeds.GetByURL(srv.URL)
	if err != nil {
		t.Fatalf("GetByURL() error = %v", err)
	}
	if got.Title != "Added" || got.Folder != "Tech" {
		t.Errorf("feed = %+v, want Title=Added Folder=Tech", got)
	}
}

func TestHandleAddFeed_InvalidURLRedisplaysFormWithError(t *testing.T) {
	h, feeds, _ := newTestHandler(t)

	closedSrv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	closedURL := closedSrv.URL
	closedSrv.Close()

	rec := postForm(h, "/feeds", url.Values{"url": {closedURL}})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "fetch feed") {
		t.Errorf("body does not surface add-feed error: %s", rec.Body.String())
	}

	all, err := feeds.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(all) != 0 {
		t.Errorf("List() = %d feeds, want 0 after a rejected add", len(all))
	}
}

func TestHandleEditFeedForm_RendersCurrentValues(t *testing.T) {
	h, feeds, _ := newTestHandler(t)
	f := &model.Feed{Title: "Original", FeedURL: "https://example.com/feed.xml", Folder: "News"}
	if err := feeds.Create(f); err != nil {
		t.Fatalf("create feed: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/feeds/"+strconv.FormatInt(f.ID, 10)+"/edit", nil)
	h.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Original") {
		t.Errorf("edit form does not show current title: %s", rec.Body.String())
	}
}

func TestHandleUpdateFeed_ChangesFolderAndSiteURLNotTitle(t *testing.T) {
	h, feeds, _ := newTestHandler(t)
	f := &model.Feed{Title: "Original", FeedURL: "https://example.com/feed.xml", Folder: "News"}
	if err := feeds.Create(f); err != nil {
		t.Fatalf("create feed: %v", err)
	}

	rec := postForm(h, "/feeds/"+strconv.FormatInt(f.ID, 10)+"/edit", url.Values{
		"title": {"Renamed"}, "folder": {"Tech"}, "site_url": {"https://renamed.example.com"},
		"feed_url": {"https://example.com/feed.xml"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}

	got, err := feeds.Get(f.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Title != "Original" || got.Folder != "Tech" || got.SiteURL != "https://renamed.example.com" {
		t.Errorf("feed after update = %+v, want Title=Original (unchanged) Folder=Tech SiteURL=https://renamed.example.com", got)
	}
}

func TestHandleUpdateFeed_ChangesFeedURL(t *testing.T) {
	h, feeds, _ := newTestHandler(t)
	f := &model.Feed{Title: "Original", FeedURL: "https://example.com/feed.xml", Folder: "News"}
	if err := feeds.Create(f); err != nil {
		t.Fatalf("create feed: %v", err)
	}

	rec := postForm(h, "/feeds/"+strconv.FormatInt(f.ID, 10)+"/edit", url.Values{
		"folder": {"News"}, "feed_url": {"https://example.com/new-feed.xml"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}

	got, err := feeds.Get(f.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.FeedURL != "https://example.com/new-feed.xml" {
		t.Errorf("FeedURL = %q, want https://example.com/new-feed.xml", got.FeedURL)
	}
}

func TestHandleUpdateFeed_FeedURLCollisionShowsFormError(t *testing.T) {
	h, feeds, _ := newTestHandler(t)
	other := &model.Feed{Title: "Other", FeedURL: "https://example.com/other.xml"}
	if err := feeds.Create(other); err != nil {
		t.Fatalf("create feed: %v", err)
	}
	f := &model.Feed{Title: "Original", FeedURL: "https://example.com/feed.xml"}
	if err := feeds.Create(f); err != nil {
		t.Fatalf("create feed: %v", err)
	}

	rec := postForm(h, "/feeds/"+strconv.FormatInt(f.ID, 10)+"/edit", url.Values{
		"folder": {""}, "feed_url": {"https://example.com/other.xml"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "already subscribed") {
		t.Errorf("body = %s, want form error mentioning already subscribed", rec.Body.String())
	}

	got, err := feeds.Get(f.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.FeedURL != "https://example.com/feed.xml" {
		t.Errorf("FeedURL changed to %q, want unchanged", got.FeedURL)
	}
}

func TestHandleDeleteFeed_RemovesFeedAndCascadesArticles(t *testing.T) {
	h, feeds, articles := newTestHandler(t)
	f := &model.Feed{Title: "ToDelete", FeedURL: "https://example.com/feed.xml"}
	if err := feeds.Create(f); err != nil {
		t.Fatalf("create feed: %v", err)
	}
	a := &model.Article{FeedID: f.ID, Title: "A1", GUID: "1"}
	if err := articles.Create(a); err != nil {
		t.Fatalf("create article: %v", err)
	}

	rec := postForm(h, "/feeds/"+strconv.FormatInt(f.ID, 10)+"/delete", url.Values{})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}

	if _, err := feeds.Get(f.ID); !errors.Is(err, db.ErrNotFound) {
		t.Errorf("Get(deleted feed) error = %v, want ErrNotFound", err)
	}
	if _, err := articles.Get(a.ID); !errors.Is(err, db.ErrNotFound) {
		t.Errorf("Get(cascaded article) error = %v, want ErrNotFound", err)
	}
}

func TestHandleDeleteFeed_UnknownFeedStillRendersIndex(t *testing.T) {
	h, _, _ := newTestHandler(t)

	rec := postForm(h, "/feeds/999/delete", url.Values{})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleMergeFeedForm_NoCandidateRedirectsToFeed(t *testing.T) {
	h, feeds, _ := newTestHandler(t)
	f := &model.Feed{Title: "Solo", FeedURL: "https://example.com/feed.xml"}
	if err := feeds.Create(f); err != nil {
		t.Fatalf("create feed: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/feeds/"+strconv.FormatInt(f.ID, 10)+"/merge", nil)
	h.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	if want := "/feeds/" + strconv.FormatInt(f.ID, 10); rec.Header().Get("Location") != want {
		t.Errorf("Location = %q, want %q", rec.Header().Get("Location"), want)
	}
}

func TestHandleMergeFeedForm_ShowsBothCandidates(t *testing.T) {
	h, feeds, _ := newTestHandler(t)
	a := &model.Feed{Title: "Feed A", FeedURL: "https://a.example.com/feed.xml"}
	if err := feeds.Create(a); err != nil {
		t.Fatalf("create feed a: %v", err)
	}
	b := &model.Feed{Title: "Feed B", FeedURL: "https://b.example.com/feed.xml"}
	if err := feeds.Create(b); err != nil {
		t.Fatalf("create feed b: %v", err)
	}
	a.MergeCandidateID = &b.ID
	if err := feeds.Update(a); err != nil {
		t.Fatalf("update feed a: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/feeds/"+strconv.FormatInt(a.ID, 10)+"/merge", nil)
	h.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Feed A") || !strings.Contains(body, "Feed B") {
		t.Errorf("merge form does not show both feeds: %s", body)
	}
}

func TestHandleMergeFeed_MergesArticlesAndDeletesLoser(t *testing.T) {
	h, feeds, articles := newTestHandler(t)
	survivor := &model.Feed{Title: "Survivor", FeedURL: "https://a.example.com/feed.xml"}
	if err := feeds.Create(survivor); err != nil {
		t.Fatalf("create survivor: %v", err)
	}
	loser := &model.Feed{Title: "Loser", FeedURL: "https://b.example.com/feed.xml"}
	if err := feeds.Create(loser); err != nil {
		t.Fatalf("create loser: %v", err)
	}
	survivor.MergeCandidateID = &loser.ID
	if err := feeds.Update(survivor); err != nil {
		t.Fatalf("update survivor: %v", err)
	}
	loser.MergeCandidateID = &survivor.ID
	if err := feeds.Update(loser); err != nil {
		t.Fatalf("update loser: %v", err)
	}
	a := &model.Article{FeedID: loser.ID, Title: "Article", GUID: "g1"}
	if err := articles.Create(a); err != nil {
		t.Fatalf("create article: %v", err)
	}

	rec := postForm(h, "/feeds/"+strconv.FormatInt(survivor.ID, 10)+"/merge", url.Values{
		"survivor_id": {strconv.FormatInt(survivor.ID, 10)},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}

	if _, err := feeds.Get(loser.ID); !errors.Is(err, db.ErrNotFound) {
		t.Errorf("Get(loser) error = %v, want ErrNotFound", err)
	}
	got, err := articles.Get(a.ID)
	if err != nil {
		t.Fatalf("Get(article) error = %v", err)
	}
	if got.FeedID != survivor.ID {
		t.Errorf("article.FeedID = %d, want %d", got.FeedID, survivor.ID)
	}
	if !strings.Contains(rec.Body.String(), "Article") {
		t.Errorf("response does not show the merged-in article: %s", rec.Body.String())
	}
}

func TestHandleMergeFeed_InvalidSurvivorIDReturns400(t *testing.T) {
	h, feeds, _ := newTestHandler(t)
	a := &model.Feed{Title: "Feed A", FeedURL: "https://a.example.com/feed.xml"}
	if err := feeds.Create(a); err != nil {
		t.Fatalf("create feed a: %v", err)
	}
	b := &model.Feed{Title: "Feed B", FeedURL: "https://b.example.com/feed.xml"}
	if err := feeds.Create(b); err != nil {
		t.Fatalf("create feed b: %v", err)
	}
	a.MergeCandidateID = &b.ID
	if err := feeds.Update(a); err != nil {
		t.Fatalf("update feed a: %v", err)
	}

	rec := postForm(h, "/feeds/"+strconv.FormatInt(a.ID, 10)+"/merge", url.Values{
		"survivor_id": {"999999"},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body: %s", rec.Code, rec.Body.String())
	}
}
