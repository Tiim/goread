package db

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Tiim/goread/internal/model"
)

// ErrNotFound is returned when a requested row does not exist.
var ErrNotFound = errors.New("not found")

// FeedRepo provides CRUD access to the feeds table.
type FeedRepo struct {
	db *sql.DB
}

// NewFeedRepo creates a FeedRepo backed by the given database handle.
func NewFeedRepo(sqlDB *sql.DB) *FeedRepo {
	return &FeedRepo{db: sqlDB}
}

func timeToNullString(t *time.Time) sql.NullString {
	if t == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: t.UTC().Format(time.RFC3339Nano), Valid: true}
}

func nullStringToTime(ns sql.NullString) (*time.Time, error) {
	if !ns.Valid || ns.String == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339Nano, ns.String)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// Create inserts a new feed and sets its assigned ID on the passed struct.
func (r *FeedRepo) Create(f *model.Feed) error {
	res, err := r.db.Exec(`INSERT INTO feeds (
		title, description, feed_url, site_url, favicon, favicon_content_type, refresh_ttl_seconds,
		etag, last_modified, folder, last_refresh_at, last_success_at, refresh_error
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		f.Title, f.Description, f.FeedURL, f.SiteURL, f.Favicon, f.FaviconContentType, int64(f.RefreshTTL/time.Second),
		f.ETag, f.LastModified, f.Folder, timeToNullString(f.LastRefreshAt), timeToNullString(f.LastSuccessAt), f.RefreshError,
	)
	if err != nil {
		return fmt.Errorf("insert feed: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("get inserted feed id: %w", err)
	}
	f.ID = id
	return nil
}

// Update overwrites all mutable fields of an existing feed, identified by ID.
func (r *FeedRepo) Update(f *model.Feed) error {
	res, err := r.db.Exec(`UPDATE feeds SET
		title = ?, description = ?, feed_url = ?, site_url = ?, favicon = ?, favicon_content_type = ?,
		refresh_ttl_seconds = ?, etag = ?, last_modified = ?, folder = ?,
		last_refresh_at = ?, last_success_at = ?, refresh_error = ?
		WHERE id = ?`,
		f.Title, f.Description, f.FeedURL, f.SiteURL, f.Favicon, f.FaviconContentType, int64(f.RefreshTTL/time.Second),
		f.ETag, f.LastModified, f.Folder, timeToNullString(f.LastRefreshAt), timeToNullString(f.LastSuccessAt), f.RefreshError,
		f.ID,
	)
	if err != nil {
		return fmt.Errorf("update feed: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("get update feed rows affected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Get retrieves a feed by ID.
func (r *FeedRepo) Get(id int64) (*model.Feed, error) {
	row := r.db.QueryRow(`SELECT
		id, title, description, feed_url, site_url, favicon, favicon_content_type, refresh_ttl_seconds,
		etag, last_modified, folder, last_refresh_at, last_success_at, refresh_error
		FROM feeds WHERE id = ?`, id)
	return scanFeed(row)
}

// GetByURL retrieves a feed by its feed URL.
func (r *FeedRepo) GetByURL(feedURL string) (*model.Feed, error) {
	row := r.db.QueryRow(`SELECT
		id, title, description, feed_url, site_url, favicon, favicon_content_type, refresh_ttl_seconds,
		etag, last_modified, folder, last_refresh_at, last_success_at, refresh_error
		FROM feeds WHERE feed_url = ?`, feedURL)
	return scanFeed(row)
}

// List returns all feeds ordered by folder then title.
func (r *FeedRepo) List() ([]*model.Feed, error) {
	rows, err := r.db.Query(`SELECT
		id, title, description, feed_url, site_url, favicon, favicon_content_type, refresh_ttl_seconds,
		etag, last_modified, folder, last_refresh_at, last_success_at, refresh_error
		FROM feeds ORDER BY folder, title`)
	if err != nil {
		return nil, fmt.Errorf("list feeds: %w", err)
	}
	defer rows.Close()

	var feeds []*model.Feed
	for rows.Next() {
		f, err := scanFeedRow(rows)
		if err != nil {
			return nil, err
		}
		feeds = append(feeds, f)
	}
	return feeds, rows.Err()
}

// Delete removes a feed by ID. Associated articles are removed via the
// foreign key's ON DELETE CASCADE.
func (r *FeedRepo) Delete(id int64) error {
	res, err := r.db.Exec(`DELETE FROM feeds WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete feed: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("get delete feed rows affected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanFeed(row *sql.Row) (*model.Feed, error) {
	f, err := scanFeedRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return f, err
}

func scanFeedRow(s rowScanner) (*model.Feed, error) {
	var f model.Feed
	var ttlSeconds int64
	var lastRefreshAt, lastSuccessAt sql.NullString
	if err := s.Scan(
		&f.ID, &f.Title, &f.Description, &f.FeedURL, &f.SiteURL, &f.Favicon, &f.FaviconContentType, &ttlSeconds,
		&f.ETag, &f.LastModified, &f.Folder, &lastRefreshAt, &lastSuccessAt, &f.RefreshError,
	); err != nil {
		return nil, fmt.Errorf("scan feed: %w", err)
	}
	f.RefreshTTL = time.Duration(ttlSeconds) * time.Second

	var err error
	if f.LastRefreshAt, err = nullStringToTime(lastRefreshAt); err != nil {
		return nil, fmt.Errorf("parse last_refresh_at: %w", err)
	}
	if f.LastSuccessAt, err = nullStringToTime(lastSuccessAt); err != nil {
		return nil, fmt.Errorf("parse last_success_at: %w", err)
	}
	return &f, nil
}
