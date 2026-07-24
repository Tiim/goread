package model

import "time"

// ArticleState indicates whether an article is still present in its source
// feed or has been removed from it (while remaining in the local database).
type ArticleState string

const (
	ArticleStatePresent ArticleState = "present"
	ArticleStateDeleted ArticleState = "deleted"
)

// Article represents a single feed entry stored locally.
type Article struct {
	ID          int64
	FeedID      int64
	GUID        string
	Title       string
	Author      string
	PublishedAt *time.Time
	UpdatedAt   *time.Time
	Link        string
	Summary     string
	Content     string
	ContentText string
	ContentType string
	Read        bool
	State       ArticleState
	ContentHash string
	Metadata    string
}
