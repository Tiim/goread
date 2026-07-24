package model

import "time"

// Feed represents a subscribed RSS/Atom feed.
type Feed struct {
	ID                 int64
	Title              string
	Description        string
	FeedURL            string
	SiteURL            string
	Favicon            []byte
	FaviconContentType string
	RefreshTTL         time.Duration
	ETag               string
	LastModified       string
	Folder             string
	LastRefreshAt      *time.Time
	LastSuccessAt      *time.Time
	RefreshError       string
	// MergeCandidateID points at another feed the refresh collision guard
	// found sharing this feed's canonical feed_url (see
	// internal/feed/refresh.go), offered to the user as a "merge feeds"
	// suggestion. Nil when there's no known duplicate.
	MergeCandidateID *int64
}
