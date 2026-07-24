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
	"github.com/Tiim/goread/internal/imageproxy"
	"github.com/Tiim/goread/internal/model"
	"github.com/Tiim/goread/internal/sanitize"
)

// pageCSP is applied to every full-page response. No JavaScript exists yet
// (that lands in Phase 6 as HTMX), so script-src is disabled outright.
// frame-src/img-src are scoped to 'self' since article content and images
// are always served back through this same origin (see article content and
// image-proxy handlers below), never fetched directly by the browser from a
// third party, per docs/spec.md "Security".
const pageCSP = "default-src 'self'; script-src 'none'; object-src 'none'; base-uri 'none'; " +
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
}

// NewHandler builds a Handler backed by the given repositories.
func NewHandler(feeds *db.FeedRepo, articles *db.ArticleRepo) *Handler {
	return &Handler{
		feeds:     feeds,
		articles:  articles,
		templates: parseTemplates(),
		images:    imageproxy.New(),
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
	h.render(w, pageData{})
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

	h.render(w, pageData{
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

	h.render(w, pageData{
		SelectedFeedID:    feedID,
		SelectedFeed:      feed,
		Articles:          articles,
		SelectedArticleID: articleID,
		SelectedArticle:   article,
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

// render fills in the feed tree (present on every page) and executes the
// full three-pane layout template.
func (h *Handler) render(w http.ResponseWriter, data pageData) {
	feeds, err := h.feeds.List()
	if err != nil {
		h.serverError(w, err)
		return
	}
	data.Folders = buildFolders(feeds)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.templates.ExecuteTemplate(w, "layout", data); err != nil {
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
