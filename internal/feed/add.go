package feed

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Tiim/goread/internal/db"
	"github.com/Tiim/goread/internal/model"
)

// ErrFeedURLExists is returned by AddFeed when a feed with the given URL is
// already subscribed.
var ErrFeedURLExists = errors.New("feed url already subscribed")

// AddFeed validates feedURL by fetching and parsing it up front, so a bad or
// unreachable URL is rejected with a clear error before any feeds row is
// created, rather than silently sitting there until the next scheduled
// refresh sets RefreshError. Title always comes from the fetched feed itself
// (never user-supplied, since Syncer.Sync overwrites it from the feed on
// every subsequent refresh anyway); folder, if non-empty, overrides the
// feed's own metadata; scheduler (nil-able, e.g. in tests) is then queued for
// an immediate background refresh, mirroring how ImportOPML handles each
// feed it creates.
func AddFeed(ctx context.Context, fetcher *Fetcher, feeds *db.FeedRepo, scheduler *Scheduler, feedURL, folder string) (*model.Feed, error) {
	feedURL = strings.TrimSpace(feedURL)
	if feedURL == "" {
		return nil, errors.New("feed url is required")
	}

	if _, err := feeds.GetByURL(feedURL); err == nil {
		return nil, ErrFeedURLExists
	} else if !errors.Is(err, db.ErrNotFound) {
		return nil, fmt.Errorf("look up feed: %w", err)
	}

	fetched, err := fetcher.Fetch(ctx, feedURL, "", "")
	if err != nil {
		return nil, fmt.Errorf("fetch feed: %w", err)
	}
	parsed, err := Parse(fetched.Body)
	if err != nil {
		return nil, fmt.Errorf("parse feed: %w", err)
	}

	f := &model.Feed{
		Title:   strings.TrimSpace(parsed.Title),
		FeedURL: feedURL,
		Folder:  strings.TrimSpace(folder),
	}
	if f.Title == "" {
		f.Title = feedURL
	}
	if parsed.Link != "" {
		f.SiteURL = parsed.Link
	}

	if err := feeds.Create(f); err != nil {
		return nil, fmt.Errorf("create feed: %w", err)
	}

	if scheduler != nil {
		scheduler.TriggerRefresh(f.ID)
	}

	return f, nil
}
