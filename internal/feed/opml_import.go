package feed

import (
	"errors"
	"fmt"
	"io"

	"github.com/Tiim/goread/internal/db"
	"github.com/Tiim/goread/internal/model"
	"github.com/Tiim/goread/internal/opml"
)

// ImportResult reports the outcome of an OPML import.
type ImportResult struct {
	Created int
	Updated int
}

// ImportOPML parses an OPML document from r and applies it to feeds:
// existing feeds (matched by feed URL) have their title/folder metadata
// updated in place; feeds not already subscribed are created. Every
// imported feed is then queued for an immediate background refresh via
// scheduler, per spec ("refresh imported feeds immediately"). scheduler may
// be nil, in which case that step is skipped (e.g. in tests that don't care
// about triggering refreshes).
func ImportOPML(feeds *db.FeedRepo, scheduler *Scheduler, r io.Reader) (ImportResult, error) {
	var result ImportResult

	parsed, err := opml.Parse(r)
	if err != nil {
		return result, fmt.Errorf("parse opml: %w", err)
	}

	for _, pf := range parsed {
		if pf.FeedURL == "" {
			continue
		}

		existing, err := feeds.GetByURL(pf.FeedURL)
		if err != nil && !errors.Is(err, db.ErrNotFound) {
			return result, fmt.Errorf("look up feed %q: %w", pf.FeedURL, err)
		}

		var feedID int64
		if errors.Is(err, db.ErrNotFound) {
			f := &model.Feed{
				Title:   pf.Title,
				FeedURL: pf.FeedURL,
				SiteURL: pf.SiteURL,
				Folder:  pf.Folder,
			}
			if err := feeds.Create(f); err != nil {
				return result, fmt.Errorf("create feed %q: %w", pf.FeedURL, err)
			}
			feedID = f.ID
			result.Created++
		} else {
			existing.Title = pf.Title
			existing.Folder = pf.Folder
			if pf.SiteURL != "" {
				existing.SiteURL = pf.SiteURL
			}
			if err := feeds.Update(existing); err != nil {
				return result, fmt.Errorf("update feed %q: %w", pf.FeedURL, err)
			}
			feedID = existing.ID
			result.Updated++
		}

		if scheduler != nil {
			scheduler.TriggerRefresh(feedID)
		}
	}

	return result, nil
}
