package feed

import (
	"errors"
	"fmt"

	"github.com/Tiim/goread/internal/db"
	"github.com/Tiim/goread/internal/model"
	"github.com/mmcdole/gofeed"
)

// Syncer synchronizes parsed feed content into the database.
type Syncer struct {
	Feeds    *db.FeedRepo
	Articles *db.ArticleRepo
}

// NewSyncer creates a Syncer backed by the given repositories.
func NewSyncer(feeds *db.FeedRepo, articles *db.ArticleRepo) *Syncer {
	return &Syncer{Feeds: feeds, Articles: articles}
}

// SyncResult reports the outcome of a single Sync call.
type SyncResult struct {
	Created int
	Updated int
	Deleted int // articles marked as no longer present in the feed
}

// Sync applies a parsed feed to the database: it updates the feed's own
// metadata, then reconciles its articles using the GUID -> Link ->
// content-hash identity chain. Existing articles are updated in place
// (preserving their ID and read state); new articles are inserted; articles
// that previously existed but are no longer present in the feed are marked
// as deleted rather than removed, per spec.
func (s *Syncer) Sync(feedID int64, parsed *gofeed.Feed) (SyncResult, error) {
	var result SyncResult

	f, err := s.Feeds.Get(feedID)
	if err != nil {
		return result, fmt.Errorf("load feed: %w", err)
	}
	applyFeedMetadata(f, parsed)
	if err := s.Feeds.Update(f); err != nil {
		return result, fmt.Errorf("update feed metadata: %w", err)
	}

	existing, err := s.Articles.ListByFeed(feedID)
	if err != nil {
		return result, fmt.Errorf("list existing articles: %w", err)
	}

	seen := make(map[int64]bool, len(parsed.Items))
	for _, item := range parsed.Items {
		article := itemToArticle(feedID, item)

		match, err := s.Articles.FindByIdentity(feedID, article.GUID, article.Link, article.ContentHash)
		if errors.Is(err, db.ErrNotFound) {
			if err := s.Articles.Create(article); err != nil {
				return result, fmt.Errorf("create article: %w", err)
			}
			result.Created++
			continue
		}
		if err != nil {
			return result, fmt.Errorf("find article identity: %w", err)
		}

		article.ID = match.ID
		article.Read = match.Read
		if err := s.Articles.Update(article); err != nil {
			return result, fmt.Errorf("update article: %w", err)
		}
		result.Updated++
		seen[match.ID] = true
	}

	for _, a := range existing {
		if seen[a.ID] || a.State == model.ArticleStateDeleted {
			continue
		}
		a.State = model.ArticleStateDeleted
		if err := s.Articles.Update(a); err != nil {
			return result, fmt.Errorf("mark article deleted: %w", err)
		}
		result.Deleted++
	}

	return result, nil
}

// applyFeedMetadata refreshes the feed's title, description, and site URL
// from the parsed feed content.
func applyFeedMetadata(f *model.Feed, parsed *gofeed.Feed) {
	f.Title = parsed.Title
	f.Description = parsed.Description
	f.SiteURL = parsed.Link
}
