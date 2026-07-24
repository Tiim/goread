package server

import (
	"errors"
	"html/template"
	"log"
	"net/http"
	"strconv"

	"github.com/Tiim/goread/internal/db"
	"github.com/Tiim/goread/internal/model"
)

// Handler serves GoRead's read-only, server-rendered web UI: a three-pane
// layout showing the folder/feed tree, the selected feed's article list, and
// the selected article's content.
type Handler struct {
	feeds     *db.FeedRepo
	articles  *db.ArticleRepo
	templates *template.Template
}

// NewHandler builds a Handler backed by the given repositories.
func NewHandler(feeds *db.FeedRepo, articles *db.ArticleRepo) *Handler {
	return &Handler{feeds: feeds, articles: articles, templates: parseTemplates()}
}

// Routes returns the HTTP handler serving the UI and its static assets.
func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.FileServerFS(staticFS))
	mux.HandleFunc("GET /{$}", h.handleIndex)
	mux.HandleFunc("GET /feeds/{id}", h.handleFeed)
	mux.HandleFunc("GET /feeds/{id}/articles/{articleID}", h.handleArticle)
	return mux
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
