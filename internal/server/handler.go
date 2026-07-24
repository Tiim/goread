package server

import (
	"database/sql"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/Tiim/goread/internal/db"
	"github.com/Tiim/goread/internal/feed"
	"github.com/Tiim/goread/internal/imageproxy"
	"github.com/Tiim/goread/internal/model"
	"github.com/Tiim/goread/internal/opml"
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
const frameCSP = "default-src 'none'; img-src 'self'; style-src 'unsafe-inline'; " +
	"sandbox allow-popups allow-popups-to-escape-sandbox"

// Handler serves GoRead's read-only, server-rendered web UI: a three-pane
// layout showing the folder/feed tree, the selected feed's article list, and
// the selected article's content.
type Handler struct {
	feeds     *db.FeedRepo
	articles  *db.ArticleRepo
	templates *template.Template
	images    *imageproxy.Client
	scheduler *feed.Scheduler
	fetcher   *feed.Fetcher
	sqlDB     *sql.DB
}

// NewHandler builds a Handler backed by the given repositories. scheduler
// may be nil (e.g. in tests that don't exercise manual refresh), in which
// case the manual refresh route is a no-op that just re-renders current
// state. sqlDB backs the /backup download route; it may be nil in tests that
// don't exercise it.
func NewHandler(feeds *db.FeedRepo, articles *db.ArticleRepo, scheduler *feed.Scheduler, sqlDB *sql.DB) *Handler {
	return &Handler{
		feeds:     feeds,
		articles:  articles,
		templates: parseTemplates(),
		images:    imageproxy.New(),
		scheduler: scheduler,
		fetcher:   feed.NewFetcher(),
		sqlDB:     sqlDB,
	}
}

// Routes returns the HTTP handler serving the UI and its static assets.
func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.FileServerFS(staticFS))
	mux.HandleFunc("GET /{$}", h.handleIndex)
	mux.HandleFunc("POST /feeds", h.handleAddFeed)
	mux.HandleFunc("GET /feeds/{id}", h.handleFeed)
	mux.HandleFunc("GET /feeds/{id}/edit", h.handleEditFeedForm)
	mux.HandleFunc("POST /feeds/{id}/edit", h.handleUpdateFeed)
	mux.HandleFunc("POST /feeds/{id}/delete", h.handleDeleteFeed)
	mux.HandleFunc("GET /feeds/{id}/merge", h.handleMergeFeedForm)
	mux.HandleFunc("POST /feeds/{id}/merge", h.handleMergeFeed)
	mux.HandleFunc("GET /feeds/{id}/articles/{articleID}", h.handleArticle)
	mux.HandleFunc("GET /feeds/{id}/articles/{articleID}/content", h.handleArticleFrame)
	mux.HandleFunc("POST /feeds/{id}/articles/{articleID}/read", h.handleSetArticleRead)
	mux.HandleFunc("POST /feeds/{id}/mark-all-read", h.handleMarkAllRead)
	mux.HandleFunc("POST /feeds/{id}/refresh", h.handleRefreshFeed)
	mux.HandleFunc("GET /feeds/{id}/favicon", h.handleFeedFavicon)
	mux.HandleFunc("GET /proxy/image", h.handleImageProxy)
	mux.HandleFunc("GET /search", h.handleSearch)
	mux.HandleFunc("GET /opml/export", h.handleOPMLExport)
	mux.HandleFunc("POST /opml/import", h.handleOPMLImport)
	mux.HandleFunc("GET /backup", h.handleBackup)
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
	// SearchActive and SearchQuery/SearchResults are set by handleSearch;
	// the article list pane renders SearchResults instead of Articles
	// whenever SearchActive is true (a search spans multiple feeds, so it
	// isn't tied to a single SelectedFeed).
	SearchActive  bool
	SearchQuery   string
	SearchResults []*model.SearchResult
	// AddFeed* fields let the "add feed" form (in feed_tree.html) redisplay
	// what the user entered alongside a validation error, instead of losing
	// their input on a rejected submission.
	AddFeedError  string
	AddFeedURL    string
	AddFeedFolder string
	// EditFeed* fields are set by handleEditFeedForm/handleUpdateFeed; the
	// article-list pane renders the edit form in place of the article list
	// whenever EditFeedActive is true.
	EditFeedActive bool
	EditFeed       *model.Feed
	EditFeedError  string
	// Merge* fields are set by handleMergeFeedForm; the article-list pane
	// renders the merge confirmation form in place of the article list
	// whenever MergeActive is true (see Phase 10, feed_tree.html's merge
	// indicator on feeds with a MergeCandidateID).
	MergeActive bool
	MergeFeed   *model.Feed
	MergeOther  *model.Feed
	MergeError  string
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

// handleAddFeed handles the "add feed" form in feed_tree.html: it validates
// and creates a new feed via feed.AddFeed (which fetches/parses the URL up
// front so a bad one is rejected with a clear error rather than silently
// sitting there until the next scheduled refresh), then jumps straight to
// the new feed's page. On failure, the form's entered values and error are
// redisplayed rather than losing the user's input.
func (h *Handler) handleAddFeed(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	feedURL := r.FormValue("url")
	folder := r.FormValue("folder")

	f, err := feed.AddFeed(r.Context(), h.fetcher, h.feeds, h.scheduler, feedURL, folder)
	if err != nil {
		h.render(w, r, pageData{
			AddFeedError:  err.Error(),
			AddFeedURL:    feedURL,
			AddFeedFolder: folder,
		})
		return
	}

	h.render(w, r, pageData{
		SelectedFeedID: f.ID,
		SelectedFeed:   f,
	})
}

// handleEditFeedForm renders the "edit feed" form (title/folder/site URL) in
// place of the article list pane.
func (h *Handler) handleEditFeedForm(w http.ResponseWriter, r *http.Request) {
	feedID, ok := parseID(w, r.PathValue("id"))
	if !ok {
		return
	}
	f, err := h.feeds.Get(feedID)
	if errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		h.serverError(w, err)
		return
	}

	h.render(w, r, pageData{
		SelectedFeedID: feedID,
		SelectedFeed:   f,
		EditFeedActive: true,
		EditFeed:       f,
	})
}

// handleUpdateFeed saves the "edit feed" form: folder/site URL are plain
// columns, so this is a straightforward FeedRepo.Update, followed by
// re-rendering the feed tree (buildFolders groups by the folder column at
// render time, so a folder change is picked up immediately). Title is not
// user-editable here — it's always sourced from the feed itself (see
// Syncer.applyFeedMetadata, which overwrites it on every refresh anyway).
func (h *Handler) handleUpdateFeed(w http.ResponseWriter, r *http.Request) {
	feedID, ok := parseID(w, r.PathValue("id"))
	if !ok {
		return
	}
	f, err := h.feeds.Get(feedID)
	if errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		h.serverError(w, err)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	f.Folder = strings.TrimSpace(r.FormValue("folder"))
	f.SiteURL = strings.TrimSpace(r.FormValue("site_url"))
	if err := h.feeds.Update(f); err != nil {
		h.serverError(w, err)
		return
	}

	h.render(w, r, pageData{
		SelectedFeedID: feedID,
		SelectedFeed:   f,
	})
}

// handleDeleteFeed deletes a feed (its articles cascade via the FK) after
// the UI's confirmation prompt, then falls back to the index page since the
// just-deleted feed can no longer be selected.
func (h *Handler) handleDeleteFeed(w http.ResponseWriter, r *http.Request) {
	feedID, ok := parseID(w, r.PathValue("id"))
	if !ok {
		return
	}
	if err := h.feeds.Delete(feedID); err != nil && !errors.Is(err, db.ErrNotFound) {
		h.serverError(w, err)
		return
	}

	h.render(w, r, pageData{})
}

// handleMergeFeedForm renders the "merge feeds" confirmation form (in place
// of the article list pane) for a feed that Refresher.Refresh flagged as
// sharing its canonical feed_url with another feed (see
// Feed.MergeCandidateID). If the feed has no pending candidate (e.g. a stale
// link, or the candidate was already merged away by someone else), it falls
// back to the feed's normal page instead of erroring.
func (h *Handler) handleMergeFeedForm(w http.ResponseWriter, r *http.Request) {
	feedID, ok := parseID(w, r.PathValue("id"))
	if !ok {
		return
	}
	f, err := h.feeds.Get(feedID)
	if errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		h.serverError(w, err)
		return
	}
	if f.MergeCandidateID == nil {
		http.Redirect(w, r, fmt.Sprintf("/feeds/%d", feedID), http.StatusSeeOther)
		return
	}
	other, err := h.feeds.Get(*f.MergeCandidateID)
	if errors.Is(err, db.ErrNotFound) {
		http.Redirect(w, r, fmt.Sprintf("/feeds/%d", feedID), http.StatusSeeOther)
		return
	} else if err != nil {
		h.serverError(w, err)
		return
	}

	h.render(w, r, pageData{
		SelectedFeedID: feedID,
		SelectedFeed:   f,
		MergeActive:    true,
		MergeFeed:      f,
		MergeOther:     other,
	})
}

// handleMergeFeed performs the merge the user confirmed in the form above:
// the form's survivor_id must be either {id} or its merge candidate, and the
// other one is merged into it and deleted (db.MergeFeeds). The result is the
// survivor's page, since the loser can no longer be selected.
func (h *Handler) handleMergeFeed(w http.ResponseWriter, r *http.Request) {
	feedID, ok := parseID(w, r.PathValue("id"))
	if !ok {
		return
	}
	f, err := h.feeds.Get(feedID)
	if errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		h.serverError(w, err)
		return
	}
	if f.MergeCandidateID == nil {
		http.Redirect(w, r, fmt.Sprintf("/feeds/%d", feedID), http.StatusSeeOther)
		return
	}
	otherID := *f.MergeCandidateID

	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	survivorID, err := strconv.ParseInt(r.FormValue("survivor_id"), 10, 64)
	if err != nil || (survivorID != feedID && survivorID != otherID) {
		http.Error(w, "invalid survivor_id", http.StatusBadRequest)
		return
	}
	loserID := otherID
	if survivorID == otherID {
		loserID = feedID
	}

	if err := db.MergeFeeds(h.sqlDB, survivorID, loserID); err != nil {
		h.serverError(w, err)
		return
	}

	survivor, err := h.feeds.Get(survivorID)
	if err != nil {
		h.serverError(w, err)
		return
	}
	articles, err := h.articles.ListByFeed(survivorID)
	if err != nil {
		h.serverError(w, err)
		return
	}
	h.render(w, r, pageData{
		SelectedFeedID: survivorID,
		SelectedFeed:   survivor,
		Articles:       articles,
	})
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

// searchResultLimit bounds how many matches handleSearch renders.
const searchResultLimit = 50

// handleSearch performs a full-text search (title, author, content, and
// feed name, per spec) and renders its results in place of a single feed's
// article list. An empty/whitespace query is treated as "no search yet"
// rather than an error.
func (h *Handler) handleSearch(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")

	var results []*model.SearchResult
	if strings.TrimSpace(query) != "" {
		var err error
		results, err = h.articles.Search(query, searchResultLimit)
		if err != nil {
			h.serverError(w, err)
			return
		}
	}

	h.render(w, r, pageData{
		SearchActive:  true,
		SearchQuery:   query,
		SearchResults: results,
	})
}

// handleFeedFavicon serves a feed's stored favicon blob. Favicons are
// fetched once per feed by the background refresher (internal/feed) and
// stored in the database rather than fetched live, so this never makes an
// outbound request itself.
func (h *Handler) handleFeedFavicon(w http.ResponseWriter, r *http.Request) {
	feedID, ok := parseID(w, r.PathValue("id"))
	if !ok {
		return
	}
	f, err := h.feeds.Get(feedID)
	if errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		h.serverError(w, err)
		return
	}
	if len(f.Favicon) == 0 {
		http.NotFound(w, r)
		return
	}

	contentType := f.FaviconContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Write(f.Favicon)
}

// handleOPMLExport streams all subscriptions as a downloadable OPML
// document, per spec ("OPML export").
func (h *Handler) handleOPMLExport(w http.ResponseWriter, r *http.Request) {
	feeds, err := h.feeds.List()
	if err != nil {
		h.serverError(w, err)
		return
	}

	exportFeeds := make([]opml.Feed, len(feeds))
	for i, f := range feeds {
		exportFeeds[i] = opml.Feed{Title: f.Title, FeedURL: f.FeedURL, SiteURL: f.SiteURL, Folder: f.Folder}
	}

	w.Header().Set("Content-Type", "text/x-opml; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="goread-subscriptions.opml"`)
	if err := opml.Generate(w, exportFeeds); err != nil {
		log.Printf("opml export: %v", err)
	}
}

// handleOPMLImport imports an uploaded OPML document (per spec, "OPML
// import" - preserving folder hierarchy, updating existing feeds' metadata,
// and triggering an immediate refresh of every imported feed), then
// re-renders the current page.
func (h *Handler) handleOPMLImport(w http.ResponseWriter, r *http.Request) {
	file, _, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing opml file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	if _, err := feed.ImportOPML(h.feeds, h.scheduler, file); err != nil {
		http.Error(w, "invalid opml file", http.StatusBadRequest)
		return
	}

	h.render(w, r, pageData{})
}

// handleBackup forces the download of a consistent snapshot of the SQLite
// database, per docs/spec.md "Backups".
func (h *Handler) handleBackup(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/vnd.sqlite3")
	w.Header().Set("Content-Disposition", `attachment; filename="goread-backup.sqlite"`)
	if err := db.Backup(h.sqlDB, w); err != nil {
		log.Printf("backup: %v", err)
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
