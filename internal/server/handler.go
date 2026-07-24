package server

import (
	"errors"
	"html/template"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"

	"github.com/Tiim/goread/internal/db"
	"github.com/Tiim/goread/internal/feed"
	"github.com/Tiim/goread/internal/imageproxy"
	"github.com/Tiim/goread/internal/model"
	"github.com/Tiim/goread/internal/sanitize"
)

// pageCSP is applied to every full-page response. script-src is scoped to
// 'self' so the vendored htmx.min.js (served from this origin) can run,
// while still forbidding any inline/third-party script. frame-src/img-src
// are scoped to 'self' since article content and images are always served
// back through this same origin (see article content and image-proxy
// handlers below), never fetched directly by the browser from a third
// party, per docs/spec.md "Security".
const pageCSP = "default-src 'self'; script-src 'self'; object-src 'none'; base-uri 'none'; " +
	"frame-src 'self'; img-src 'self'; style-src 'self'; form-action 'self'"

// frameCSP is applied to the sandboxed article-content document rendered
// inside the read pane's iframe. It's even stricter than pageCSP: the frame
// never needs to navigate or submit anywhere.
const frameCSP = "default-src 'none'; img-src 'self'; style-src 'unsafe-inline'; sandbox"

// Handler serves GoRead's read-only, server-rendered web UI: a three-pane
// layout showing the folder/feed tree, the selected feed's article list, and
// the selected article's content.
type Handler struct {
	feeds     *db.FeedRepo
	articles  *db.ArticleRepo
	templates *template.Template
	images    *imageproxy.Client
	scheduler *feed.Scheduler
}

// NewHandler builds a Handler backed by the given repositories. scheduler
// may be nil (e.g. in tests that don't exercise manual refresh), in which
// case the manual refresh route is a no-op that just re-renders current
// state.
func NewHandler(feeds *db.FeedRepo, articles *db.ArticleRepo, scheduler *feed.Scheduler) *Handler {
	return &Handler{
		feeds:     feeds,
		articles:  articles,
		templates: parseTemplates(),
		images:    imageproxy.New(),
		scheduler: scheduler,
	}
}

// Routes returns the HTTP handler serving the UI and its static assets.
func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.FileServerFS(staticFS))
	mux.HandleFunc("GET /{$}", h.handleIndex)
	mux.HandleFunc("GET /feeds/{id}", h.handleFeed)
	mux.HandleFunc("GET /feeds/{id}/articles/{articleID}", h.handleArticle)
	mux.HandleFunc("GET /feeds/{id}/articles/{articleID}/content", h.handleArticleFrame)
	mux.HandleFunc("POST /feeds/{id}/articles/{articleID}/read", h.handleSetArticleRead)
	mux.HandleFunc("POST /feeds/{id}/mark-all-read", h.handleMarkAllRead)
	mux.HandleFunc("POST /feeds/{id}/refresh", h.handleRefreshFeed)
	mux.HandleFunc("GET /proxy/image", h.handleImageProxy)
	return withCSP(mux)
}

// withCSP sets the strict page-level CSP on every response. The article
// frame handler overrides it with the even-stricter frameCSP for its own
// response.
func withCSP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", pageCSP)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}

type folderView struct {
	Name  string
	Feeds []*model.Feed
}

type pageData struct {
	Folders           []folderView
	SelectedFeedID    int64
	SelectedFeed      *model.Feed
	Articles          []*model.Article
	SelectedArticleID int64
	SelectedArticle   *model.Article
}

// buildFolders groups feeds (already ordered by folder, then title) into
// folderView buckets, labeling feeds with no folder as "Uncategorized".
func buildFolders(feeds []*model.Feed) []folderView {
	var folders []folderView
	for _, f := range feeds {
		name := f.Folder
		if name == "" {
			name = "Uncategorized"
		}
		if len(folders) == 0 || folders[len(folders)-1].Name != name {
			folders = append(folders, folderView{Name: name})
		}
		last := &folders[len(folders)-1]
		last.Feeds = append(last.Feeds, f)
	}
	return folders
}

func (h *Handler) handleIndex(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, pageData{})
}

func (h *Handler) handleFeed(w http.ResponseWriter, r *http.Request) {
	feedID, ok := parseID(w, r.PathValue("id"))
	if !ok {
		return
	}

	feed, err := h.feeds.Get(feedID)
	if errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		h.serverError(w, err)
		return
	}

	articles, err := h.articles.ListByFeed(feedID)
	if err != nil {
		h.serverError(w, err)
		return
	}

	h.render(w, r, pageData{
		SelectedFeedID: feedID,
		SelectedFeed:   feed,
		Articles:       articles,
	})
}

func (h *Handler) handleArticle(w http.ResponseWriter, r *http.Request) {
	feedID, ok := parseID(w, r.PathValue("id"))
	if !ok {
		return
	}
	articleID, ok := parseID(w, r.PathValue("articleID"))
	if !ok {
		return
	}
	h.renderArticlePage(w, r, feedID, articleID, true)
}

// renderArticlePage loads a feed, its article list, and one selected article,
// then renders the full page/fragment. When markRead is true and the
// article isn't already read, it's marked read first (viewing an article is
// the normal way a user marks it read; explicit toggling goes through
// handleSetArticleRead with markRead=false so it isn't immediately
// overridden here).
func (h *Handler) renderArticlePage(w http.ResponseWriter, r *http.Request, feedID, articleID int64, markRead bool) {
	feed, err := h.feeds.Get(feedID)
	if errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		h.serverError(w, err)
		return
	}

	article, err := h.articles.Get(articleID)
	if errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		h.serverError(w, err)
		return
	}
	if article.FeedID != feedID {
		http.NotFound(w, r)
		return
	}

	if markRead && !article.Read {
		if err := h.articles.SetRead(articleID, true); err != nil {
			h.serverError(w, err)
			return
		}
		article.Read = true
	}

	articles, err := h.articles.ListByFeed(feedID)
	if err != nil {
		h.serverError(w, err)
		return
	}

	h.render(w, r, pageData{
		SelectedFeedID:    feedID,
		SelectedFeed:      feed,
		Articles:          articles,
		SelectedArticleID: articleID,
		SelectedArticle:   article,
	})
}

// handleSetArticleRead toggles a single article's read state (via the
// "Mark read"/"Mark unread" button) and re-renders the article page.
func (h *Handler) handleSetArticleRead(w http.ResponseWriter, r *http.Request) {
	feedID, ok := parseID(w, r.PathValue("id"))
	if !ok {
		return
	}
	articleID, ok := parseID(w, r.PathValue("articleID"))
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	read := r.FormValue("read") == "true"

	article, err := h.articles.Get(articleID)
	if errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		h.serverError(w, err)
		return
	}
	if article.FeedID != feedID {
		http.NotFound(w, r)
		return
	}

	if err := h.articles.SetRead(articleID, read); err != nil {
		h.serverError(w, err)
		return
	}

	h.renderArticlePage(w, r, feedID, articleID, false)
}

// handleMarkAllRead marks every article in a feed as read. If the request
// carries an articleID (the currently open article, passed via hx-vals so
// selection survives the update), that article's page is re-rendered;
// otherwise the feed's article list is rendered with no selection.
func (h *Handler) handleMarkAllRead(w http.ResponseWriter, r *http.Request) {
	feedID, ok := parseID(w, r.PathValue("id"))
	if !ok {
		return
	}
	feed, err := h.feeds.Get(feedID)
	if errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		h.serverError(w, err)
		return
	}

	if err := h.articles.MarkAllReadForFeed(feedID); err != nil {
		h.serverError(w, err)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	if raw := r.FormValue("articleID"); raw != "" {
		if articleID, err := strconv.ParseInt(raw, 10, 64); err == nil {
			h.renderArticlePage(w, r, feedID, articleID, false)
			return
		}
	}

	articles, err := h.articles.ListByFeed(feedID)
	if err != nil {
		h.serverError(w, err)
		return
	}
	h.render(w, r, pageData{
		SelectedFeedID: feedID,
		SelectedFeed:   feed,
		Articles:       articles,
	})
}

// handleRefreshFeed performs an immediate, synchronous refresh of a single
// feed (the "refresh now" button) by routing through the scheduler's queue
// so it never runs concurrently with a due-feed background refresh, then
// re-renders the feed's page so any RefreshError or new articles show up
// right away.
func (h *Handler) handleRefreshFeed(w http.ResponseWriter, r *http.Request) {
	feedID, ok := parseID(w, r.PathValue("id"))
	if !ok {
		return
	}
	if _, err := h.feeds.Get(feedID); errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		h.serverError(w, err)
		return
	}

	if h.scheduler != nil {
		h.scheduler.TriggerRefreshSync(r.Context(), feedID)
	}

	feed, err := h.feeds.Get(feedID)
	if err != nil {
		h.serverError(w, err)
		return
	}
	articles, err := h.articles.ListByFeed(feedID)
	if err != nil {
		h.serverError(w, err)
		return
	}
	h.render(w, r, pageData{
		SelectedFeedID: feedID,
		SelectedFeed:   feed,
		Articles:       articles,
	})
}

// articleFrameData is passed to the standalone article-frame template
// rendered inside the sandboxed iframe.
type articleFrameData struct {
	ContentHTML template.HTML
}

// handleArticleFrame renders the sanitized article body as a standalone HTML
// document, meant to be loaded only inside the sandboxed iframe from
// article_content.html. Article content is untrusted (it comes from
// third-party feeds), so it's sanitized and its images are routed through
// the local image proxy before being embedded here.
func (h *Handler) handleArticleFrame(w http.ResponseWriter, r *http.Request) {
	feedID, ok := parseID(w, r.PathValue("id"))
	if !ok {
		return
	}
	articleID, ok := parseID(w, r.PathValue("articleID"))
	if !ok {
		return
	}

	article, err := h.articles.Get(articleID)
	if errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		h.serverError(w, err)
		return
	}
	if article.FeedID != feedID {
		http.NotFound(w, r)
		return
	}

	safe := sanitize.HTML(article.Content)
	safe, err = sanitize.RewriteImageSrcs(safe, proxyImageURL)
	if err != nil {
		h.serverError(w, err)
		return
	}

	w.Header().Set("Content-Security-Policy", frameCSP)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.templates.ExecuteTemplate(w, "article_frame", articleFrameData{ContentHTML: template.HTML(safe)}); err != nil {
		log.Printf("render article frame: %v", err)
	}
}

// proxyImageURL rewrites a (possibly remote) image src into a same-origin
// URL served by handleImageProxy, so the browser never contacts the
// third-party host directly.
func proxyImageURL(src string) string {
	u := url.URL{Path: "/proxy/image", RawQuery: url.Values{"url": {src}}.Encode()}
	return u.String()
}

// handleImageProxy fetches a remote image on the server's behalf and streams
// it back, so article content never causes the browser to make third-party
// requests directly (docs/spec.md "Images"). Responses are never cached
// permanently: the image is re-fetched on every view.
func (h *Handler) handleImageProxy(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("url")
	if raw == "" {
		http.Error(w, "missing url", http.StatusBadRequest)
		return
	}

	result, err := h.images.Fetch(r.Context(), raw)
	if err != nil {
		log.Printf("image proxy: %v", err)
		http.Error(w, "could not fetch image", http.StatusBadGateway)
		return
	}
	defer result.Body.Close()

	w.Header().Set("Content-Type", result.ContentType)
	w.Header().Set("Cache-Control", "no-store")
	if _, err := io.Copy(w, result.Body); err != nil {
		log.Printf("image proxy: copy response: %v", err)
	}
}

// render fills in the feed tree (present on every page) and executes either
// the full page layout, or - for HTMX requests, which only ever swap the
// #app div - just that fragment, saving the head/body boilerplate on every
// partial update.
func (h *Handler) render(w http.ResponseWriter, r *http.Request, data pageData) {
	feeds, err := h.feeds.List()
	if err != nil {
		h.serverError(w, err)
		return
	}
	data.Folders = buildFolders(feeds)

	tmpl := "layout"
	if r.Header.Get("HX-Request") == "true" {
		tmpl = "app"
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.templates.ExecuteTemplate(w, tmpl, data); err != nil {
		log.Printf("render template: %v", err)
	}
}

func (h *Handler) serverError(w http.ResponseWriter, err error) {
	log.Printf("server error: %v", err)
	http.Error(w, "internal server error", http.StatusInternalServerError)
}

func parseID(w http.ResponseWriter, raw string) (int64, bool) {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return 0, false
	}
	return id, true
}
