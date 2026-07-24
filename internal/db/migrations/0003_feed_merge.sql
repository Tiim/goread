-- merge_candidate_id points at another feed whose feed_url the refresh
-- collision guard (Refresher.Refresh, internal/feed/refresh.go) found already
-- in use by a different feed, i.e. a permanent-redirect convergence. It's set
-- on both feeds involved (symmetric) so the merge indicator/action is visible
-- from either one. ON DELETE SET NULL clears it automatically once a merge
-- deletes the other side.
ALTER TABLE feeds ADD COLUMN merge_candidate_id INTEGER REFERENCES feeds (id) ON DELETE SET NULL;
