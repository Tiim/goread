package model

import "time"

// Feed represents a subscribed RSS/Atom feed.
type Feed struct {
	ID            int64
	Title         string
	Description   string
	FeedURL       string
	SiteURL       string
	Favicon       []byte
	RefreshTTL    time.Duration
	ETag          string
	LastModified  string
	Folder        string
	LastRefreshAt *time.Time
	LastSuccessAt *time.Time
	RefreshError  string
}
