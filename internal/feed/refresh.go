package feed

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/Tiim/goread/internal/db"
	"github.com/Tiim/goread/internal/favicon"
	"github.com/Tiim/goread/internal/model"
	"github.com/mmcdole/gofeed"
)

// Refresher performs a full refresh cycle for a single feed: a conditional
// fetch, parse, sync, and bookkeeping of cache headers, TTL, and error
// state.
type Refresher struct {
	Fetcher *Fetcher
	Syncer  *Syncer
	Feeds   *db.FeedRepo
	Now     func() time.Time
	// Favicon fetches and downsizes feed favicons for storage (see
	// docs/spec.md "Favicons"). It may be nil to skip favicon fetching
	// entirely (e.g. in tests, to keep them offline).
	Favicon *favicon.Client
}

// NewRefresher creates a Refresher with production defaults.
func NewRefresher(feeds *db.FeedRepo, articles *db.ArticleRepo) *Refresher {
	return &Refresher{
		Fetcher: NewFetcher(),
		Syncer:  NewSyncer(feeds, articles),
		Feeds:   feeds,
		Now:     time.Now,
		Favicon: favicon.New(),
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
		if existing, err := r.Feeds.GetByURL(fetched.FinalURL); err == nil && existing.ID != f.ID {
			// Another feed already uses this URL (e.g. two feeds whose
			// permanent redirects converge on the same canonical URL).
			// Keep this feed's original URL rather than failing every
			// future refresh on a UNIQUE constraint violation, and flag both
			// feeds as merge candidates (symmetrically, so the suggestion
			// surfaces from whichever one the user happens to view) so the
			// user can explicitly merge them (see db.MergeFeeds).
			log.Printf("refresh: feed %d redirects to %s, already used by feed %d; keeping original URL", f.ID, fetched.FinalURL, existing.ID)
			f.MergeCandidateID = &existing.ID
			existing.MergeCandidateID = &f.ID
			if err := r.Feeds.Update(existing); err != nil {
				log.Printf("refresh: record merge candidate on feed %d: %v", existing.ID, err)
			}
		} else {
			f.FeedURL = fetched.FinalURL
		}
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

	r.fetchFaviconIfMissing(ctx, feedID, parsed)

	return result, nil
}

// fetchFaviconIfMissing fetches and persists a feed's favicon the first time
// it's seen without one, following the spec's priority order (Atom <icon> /
// RSS <image>, exposed on the parsed feed as Image.URL, falling back to
// site_url/favicon.ico - see internal/favicon). Favicons are never
// refetched once stored, and any fetch failure is logged and otherwise
// ignored: it must never fail the refresh itself.
func (r *Refresher) fetchFaviconIfMissing(ctx context.Context, feedID int64, parsed *gofeed.Feed) {
	if r.Favicon == nil {
		return
	}
	f, err := r.Feeds.Get(feedID)
	if err != nil || len(f.Favicon) > 0 {
		return
	}

	var candidate string
	if parsed.Image != nil {
		candidate = parsed.Image.URL
	}
	res, err := r.Favicon.Fetch(ctx, candidate, f.SiteURL)
	if err != nil {
		log.Printf("refresh: fetch favicon for feed %d: %v", feedID, err)
		return
	}
	if res == nil {
		return
	}

	f.Favicon = res.Data
	f.FaviconContentType = res.ContentType
	if err := r.Feeds.Update(f); err != nil {
		log.Printf("refresh: persist favicon for feed %d: %v", feedID, err)
	}
}
