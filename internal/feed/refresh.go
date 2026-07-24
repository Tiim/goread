package feed

import (
	"context"
	"fmt"
	"time"

	"github.com/Tiim/goread/internal/db"
	"github.com/Tiim/goread/internal/model"
)

// Refresher performs a full refresh cycle for a single feed: a conditional
// fetch, parse, sync, and bookkeeping of cache headers, TTL, and error
// state.
type Refresher struct {
	Fetcher *Fetcher
	Syncer  *Syncer
	Feeds   *db.FeedRepo
	Now     func() time.Time
}

// NewRefresher creates a Refresher with production defaults.
func NewRefresher(feeds *db.FeedRepo, articles *db.ArticleRepo) *Refresher {
	return &Refresher{
		Fetcher: NewFetcher(),
		Syncer:  NewSyncer(feeds, articles),
		Feeds:   feeds,
		Now:     time.Now,
	}
}

// Due reports whether f is ready to be refreshed again, based on its last
// refresh time and TTL (or DefaultTTL if it has none recorded yet, e.g. a
// newly added feed that has never been fetched).
func Due(f *model.Feed, now time.Time) bool {
	if f.LastRefreshAt == nil {
		return true
	}
	ttl := f.RefreshTTL
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	return now.Sub(*f.LastRefreshAt) >= ttl
}

// Refresh fetches, parses, and synchronizes a single feed by ID.
//
// Fetch and parse failures are recorded on the feed as RefreshError and
// leave existing feed/article data unchanged, per spec; such failures are
// reported via the feed's error state, not as a returned error, so a caller
// refreshing many feeds can continue past individual failures. A returned
// error means the feed itself could not be loaded or its state could not be
// persisted.
func (r *Refresher) Refresh(ctx context.Context, feedID int64) (SyncResult, error) {
	var result SyncResult

	f, err := r.Feeds.Get(feedID)
	if err != nil {
		return result, fmt.Errorf("load feed: %w", err)
	}

	now := r.Now()
	f.LastRefreshAt = &now

	fetched, err := r.Fetcher.Fetch(ctx, f.FeedURL, f.ETag, f.LastModified)
	if err != nil {
		f.RefreshError = err.Error()
		if uerr := r.Feeds.Update(f); uerr != nil {
			return result, fmt.Errorf("record refresh error: %w", uerr)
		}
		return result, nil
	}

	if fetched.FinalURL != "" && fetched.FinalURL != f.FeedURL {
		f.FeedURL = fetched.FinalURL
	}

	if fetched.NotModified {
		f.RefreshError = ""
		f.LastSuccessAt = &now
		if err := r.Feeds.Update(f); err != nil {
			return result, fmt.Errorf("update feed after not-modified response: %w", err)
		}
		return result, nil
	}

	parsed, err := Parse(fetched.Body)
	if err != nil {
		f.RefreshError = err.Error()
		if uerr := r.Feeds.Update(f); uerr != nil {
			return result, fmt.Errorf("record parse error: %w", uerr)
		}
		return result, nil
	}

	f.ETag = fetched.ETag
	f.LastModified = fetched.LastModified
	f.RefreshTTL = TTL(parsed)
	f.RefreshError = ""
	f.LastSuccessAt = &now
	if err := r.Feeds.Update(f); err != nil {
		return result, fmt.Errorf("update feed metadata: %w", err)
	}

	result, err = r.Syncer.Sync(feedID, parsed)
	if err != nil {
		return result, fmt.Errorf("sync feed: %w", err)
	}
	return result, nil
}
