package db

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/Tiim/goread/internal/model"
)

// ArticleRepo provides CRUD access to the articles table.
type ArticleRepo struct {
	db *sql.DB
}

// NewArticleRepo creates an ArticleRepo backed by the given database handle.
func NewArticleRepo(sqlDB *sql.DB) *ArticleRepo {
	return &ArticleRepo{db: sqlDB}
}

// Create inserts a new article and sets its assigned ID on the passed struct.
func (r *ArticleRepo) Create(a *model.Article) error {
	if a.State == "" {
		a.State = model.ArticleStatePresent
	}
	res, err := r.db.Exec(`INSERT INTO articles (
		feed_id, guid, title, author, published_at, updated_at, link, summary,
		content, content_text, content_type, read, state, content_hash, metadata
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.FeedID, a.GUID, a.Title, a.Author, timeToNullString(a.PublishedAt), timeToNullString(a.UpdatedAt),
		a.Link, a.Summary, a.Content, a.ContentText, a.ContentType, a.Read, string(a.State), a.ContentHash, a.Metadata,
	)
	if err != nil {
		return fmt.Errorf("insert article: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("get inserted article id: %w", err)
	}
	a.ID = id
	return nil
}

// Update overwrites all mutable fields of an existing article, identified by ID.
func (r *ArticleRepo) Update(a *model.Article) error {
	res, err := r.db.Exec(`UPDATE articles SET
		feed_id = ?, guid = ?, title = ?, author = ?, published_at = ?, updated_at = ?,
		link = ?, summary = ?, content = ?, content_text = ?, content_type = ?,
		read = ?, state = ?, content_hash = ?, metadata = ?
		WHERE id = ?`,
		a.FeedID, a.GUID, a.Title, a.Author, timeToNullString(a.PublishedAt), timeToNullString(a.UpdatedAt),
		a.Link, a.Summary, a.Content, a.ContentText, a.ContentType, a.Read, string(a.State), a.ContentHash, a.Metadata,
		a.ID,
	)
	if err != nil {
		return fmt.Errorf("update article: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("get update article rows affected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Get retrieves an article by ID.
func (r *ArticleRepo) Get(id int64) (*model.Article, error) {
	row := r.db.QueryRow(articleSelectColumns+` FROM articles WHERE id = ?`, id)
	a, err := scanArticleRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return a, err
}

// ListByFeed returns all articles belonging to a feed, most recently
// published first.
func (r *ArticleRepo) ListByFeed(feedID int64) ([]*model.Article, error) {
	rows, err := r.db.Query(articleSelectColumns+` FROM articles WHERE feed_id = ? ORDER BY published_at DESC, id DESC`, feedID)
	if err != nil {
		return nil, fmt.Errorf("list articles by feed: %w", err)
	}
	defer rows.Close()

	var articles []*model.Article
	for rows.Next() {
		a, err := scanArticleRow(rows)
		if err != nil {
			return nil, err
		}
		articles = append(articles, a)
	}
	return articles, rows.Err()
}

// FindByIdentity looks up an existing article within a feed using the
// GUID -> Link -> content hash fallback chain. Empty candidate values are
// skipped. Returns ErrNotFound if no match exists.
func (r *ArticleRepo) FindByIdentity(feedID int64, guid, link, contentHash string) (*model.Article, error) {
	if guid != "" {
		if a, err := r.findBy(feedID, "guid", guid); err == nil {
			return a, nil
		} else if !errors.Is(err, ErrNotFound) {
			return nil, err
		}
	}
	if link != "" {
		if a, err := r.findBy(feedID, "link", link); err == nil {
			return a, nil
		} else if !errors.Is(err, ErrNotFound) {
			return nil, err
		}
	}
	if contentHash != "" {
		if a, err := r.findBy(feedID, "content_hash", contentHash); err == nil {
			return a, nil
		} else if !errors.Is(err, ErrNotFound) {
			return nil, err
		}
	}
	return nil, ErrNotFound
}

func (r *ArticleRepo) findBy(feedID int64, column, value string) (*model.Article, error) {
	row := r.db.QueryRow(articleSelectColumns+fmt.Sprintf(` FROM articles WHERE feed_id = ? AND %s = ? LIMIT 1`, column), feedID, value)
	a, err := scanArticleRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return a, err
}

// Search performs a full-text search over article titles, authors, text
// content, and feed names via the articles_fts index (see migration
// 0002_search_favicon.sql), ranked by FTS5's built-in relevance ordering.
// An empty or whitespace-only query returns no results rather than matching
// everything.
func (r *ArticleRepo) Search(query string, limit int) ([]*model.SearchResult, error) {
	match := buildFTSQuery(query)
	if match == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}

	rows, err := r.db.Query(articleSelectColumns+`, feeds.title
		FROM articles_fts
		JOIN articles ON articles.id = articles_fts.rowid
		JOIN feeds ON feeds.id = articles.feed_id
		WHERE articles_fts MATCH ?
		ORDER BY rank
		LIMIT ?`, match, limit)
	if err != nil {
		return nil, fmt.Errorf("search articles: %w", err)
	}
	defer rows.Close()

	var results []*model.SearchResult
	for rows.Next() {
		a, feedTitle, err := scanArticleSearchRow(rows)
		if err != nil {
			return nil, err
		}
		results = append(results, &model.SearchResult{Article: a, FeedTitle: feedTitle})
	}
	return results, rows.Err()
}

// buildFTSQuery converts free-form user input into a safe FTS5 MATCH
// expression: each whitespace-separated term is turned into a quoted prefix
// query and ANDed together, so arbitrary user input (which may contain FTS5
// query syntax like unbalanced quotes or operators) can never produce a
// malformed or unintended query.
func buildFTSQuery(query string) string {
	fields := strings.Fields(query)
	terms := make([]string, 0, len(fields))
	for _, f := range fields {
		escaped := strings.ReplaceAll(f, `"`, `""`)
		terms = append(terms, fmt.Sprintf(`"%s"*`, escaped))
	}
	return strings.Join(terms, " AND ")
}

// SetRead updates the read state of a single article.
func (r *ArticleRepo) SetRead(id int64, read bool) error {
	res, err := r.db.Exec(`UPDATE articles SET read = ? WHERE id = ?`, read, id)
	if err != nil {
		return fmt.Errorf("set article read state: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("get set read rows affected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// MarkAllReadForFeed marks every article in a feed as read.
func (r *ArticleRepo) MarkAllReadForFeed(feedID int64) error {
	if _, err := r.db.Exec(`UPDATE articles SET read = 1 WHERE feed_id = ?`, feedID); err != nil {
		return fmt.Errorf("mark all read for feed: %w", err)
	}
	return nil
}

const articleSelectColumns = `SELECT
	articles.id, articles.feed_id, articles.guid, articles.title, articles.author,
	articles.published_at, articles.updated_at, articles.link, articles.summary,
	articles.content, articles.content_text, articles.content_type, articles.read,
	articles.state, articles.content_hash, articles.metadata`

func scanArticleRow(s rowScanner) (*model.Article, error) {
	var a model.Article
	var publishedAt, updatedAt sql.NullString
	var state string
	if err := s.Scan(
		&a.ID, &a.FeedID, &a.GUID, &a.Title, &a.Author, &publishedAt, &updatedAt, &a.Link, &a.Summary,
		&a.Content, &a.ContentText, &a.ContentType, &a.Read, &state, &a.ContentHash, &a.Metadata,
	); err != nil {
		return nil, fmt.Errorf("scan article: %w", err)
	}
	a.State = model.ArticleState(state)

	var err error
	if a.PublishedAt, err = nullStringToTime(publishedAt); err != nil {
		return nil, fmt.Errorf("parse published_at: %w", err)
	}
	if a.UpdatedAt, err = nullStringToTime(updatedAt); err != nil {
		return nil, fmt.Errorf("parse updated_at: %w", err)
	}
	return &a, nil
}

func scanArticleSearchRow(s rowScanner) (*model.Article, string, error) {
	var a model.Article
	var publishedAt, updatedAt sql.NullString
	var state string
	var feedTitle string
	if err := s.Scan(
		&a.ID, &a.FeedID, &a.GUID, &a.Title, &a.Author, &publishedAt, &updatedAt, &a.Link, &a.Summary,
		&a.Content, &a.ContentText, &a.ContentType, &a.Read, &state, &a.ContentHash, &a.Metadata, &feedTitle,
	); err != nil {
		return nil, "", fmt.Errorf("scan article search result: %w", err)
	}
	a.State = model.ArticleState(state)

	var err error
	if a.PublishedAt, err = nullStringToTime(publishedAt); err != nil {
		return nil, "", fmt.Errorf("parse published_at: %w", err)
	}
	if a.UpdatedAt, err = nullStringToTime(updatedAt); err != nil {
		return nil, "", fmt.Errorf("parse updated_at: %w", err)
	}
	return &a, feedTitle, nil
}
