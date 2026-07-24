package db

import (
	"database/sql"
	"errors"
	"fmt"
)

// MergeFeeds merges loserID into survivorID and deletes the loser feed, all
// inside a single transaction so a failure partway through leaves both feeds
// untouched rather than half-merged (rollback safety per docs/phases.md
// Phase 10).
//
// For each of the loser's articles, an existing survivor article matching by
// the same GUID -> Link -> content hash identity chain used during normal
// sync (see ArticleRepo.FindByIdentity) is treated as the same underlying
// article: the survivor's copy is kept, marked read if either copy was, and
// the loser's copy is discarded (via the loser feed's cascade delete at the
// end). Articles with no match in the survivor are reassigned to it instead
// of being dropped.
func MergeFeeds(sqlDB *sql.DB, survivorID, loserID int64) error {
	if survivorID == loserID {
		return fmt.Errorf("merge feeds: survivor and loser are the same feed (%d)", survivorID)
	}

	tx, err := sqlDB.Begin()
	if err != nil {
		return fmt.Errorf("begin merge transaction: %w", err)
	}
	defer tx.Rollback()

	var survivorTitle string
	if err := tx.QueryRow(`SELECT title FROM feeds WHERE id = ?`, survivorID).Scan(&survivorTitle); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("load survivor feed: %w", err)
	}
	var loserExists bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM feeds WHERE id = ?)`, loserID).Scan(&loserExists); err != nil {
		return fmt.Errorf("check loser feed: %w", err)
	}
	if !loserExists {
		return ErrNotFound
	}

	type loserArticle struct {
		id                      int64
		guid, link, contentHash string
		read                    bool
	}

	rows, err := tx.Query(`SELECT id, guid, link, content_hash, read FROM articles WHERE feed_id = ?`, loserID)
	if err != nil {
		return fmt.Errorf("list loser articles: %w", err)
	}
	var loserArticles []loserArticle
	for rows.Next() {
		var a loserArticle
		if err := rows.Scan(&a.id, &a.guid, &a.link, &a.contentHash, &a.read); err != nil {
			rows.Close()
			return fmt.Errorf("scan loser article: %w", err)
		}
		loserArticles = append(loserArticles, a)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate loser articles: %w", err)
	}
	rows.Close()

	for _, a := range loserArticles {
		matchID, matchRead, found, err := findArticleByIdentityTx(tx, survivorID, a.guid, a.link, a.contentHash)
		if err != nil {
			return err
		}
		if found {
			if a.read && !matchRead {
				if _, err := tx.Exec(`UPDATE articles SET read = 1 WHERE id = ?`, matchID); err != nil {
					return fmt.Errorf("merge read state for article %d: %w", matchID, err)
				}
			}
			continue
		}

		if _, err := tx.Exec(`UPDATE articles SET feed_id = ? WHERE id = ?`, survivorID, a.id); err != nil {
			return fmt.Errorf("reassign article %d: %w", a.id, err)
		}
		// articles_fts_au (fired by the UPDATE above) refreshes
		// title/author/content_text but not feed_title, since it isn't a
		// column on articles itself - fix it up explicitly here.
		if _, err := tx.Exec(`UPDATE articles_fts SET feed_title = ? WHERE rowid = ?`, survivorTitle, a.id); err != nil {
			return fmt.Errorf("update search index feed title for article %d: %w", a.id, err)
		}
	}

	if _, err := tx.Exec(`UPDATE feeds SET merge_candidate_id = NULL WHERE id = ?`, survivorID); err != nil {
		return fmt.Errorf("clear survivor merge candidate: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM feeds WHERE id = ?`, loserID); err != nil {
		return fmt.Errorf("delete loser feed: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit merge transaction: %w", err)
	}
	return nil
}

// findArticleByIdentityTx mirrors ArticleRepo.FindByIdentity's GUID -> Link ->
// content hash fallback chain, scoped to a transaction so the merge above is
// atomic.
func findArticleByIdentityTx(tx *sql.Tx, feedID int64, guid, link, contentHash string) (id int64, read bool, found bool, err error) {
	if guid != "" {
		if id, read, found, err = queryArticleByColumnTx(tx, feedID, "guid", guid); err != nil || found {
			return
		}
	}
	if link != "" {
		if id, read, found, err = queryArticleByColumnTx(tx, feedID, "link", link); err != nil || found {
			return
		}
	}
	if contentHash != "" {
		if id, read, found, err = queryArticleByColumnTx(tx, feedID, "content_hash", contentHash); err != nil || found {
			return
		}
	}
	return 0, false, false, nil
}

func queryArticleByColumnTx(tx *sql.Tx, feedID int64, column, value string) (int64, bool, bool, error) {
	row := tx.QueryRow(fmt.Sprintf(`SELECT id, read FROM articles WHERE feed_id = ? AND %s = ? LIMIT 1`, column), feedID, value)
	var id int64
	var read bool
	if err := row.Scan(&id, &read); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, false, false, nil
		}
		return 0, false, false, fmt.Errorf("query article by %s: %w", column, err)
	}
	return id, read, true, nil
}
