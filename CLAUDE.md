# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project overview

GoRead is a lightweight, self-hosted RSS/Atom reader written in Go, for a single local user. It is distributed
as a single executable installable via `go install`, uses SQLite as its only datastore, requires no
authentication, and runs entirely on `localhost`. Full requirements are in `docs/spec.md`; the staged
implementation plan is in `docs/phases.md` — check which phase is current before starting work, and implement
phases in order. Phases 1–8 are implemented; Phase 9 (manual feed add/delete/edit-folder usability helpers) is
next, followed by Phase 10 (feed merging for convergent-URL duplicates).

## Commands

```bash
go build ./...              # build everything
go vet ./...                 # static checks
gofmt -l .                    # list files needing formatting (should be empty)
go test ./...                # run all tests
go test ./internal/db/... -run TestFeedRepo_Create -v   # run a single test
go run ./cmd/goread          # run the server locally
```

The build must stay installable with plain `go build`/`go install` — no external build system, no CGO. This is
why the SQLite driver is the pure-Go `modernc.org/sqlite` (which ships FTS5 compiled in — no build tags needed),
not `mattn/go-sqlite3`, and why feed parsing uses the pure-Go `github.com/mmcdole/gofeed` rather than a
cgo-based XML/parsing library. Favicon downsizing likewise avoids `golang.org/x/image` and hand-rolls a small
nearest-neighbor resize to keep dependencies minimal.

## Architecture

- `cmd/goread/main.go` — entrypoint. Wires together `appdir` (DB location) → `db.Open` (DB init/migration) →
  `server.Listen` (port binding) → `http.Serve`, plus starting `feed.Scheduler.Run` in a goroutine and handling
  graceful shutdown (`SIGINT`/`SIGTERM`): the HTTP server is shut down first, then `main` waits on the
  scheduler's goroutine so an in-flight refresh finishes before the DB is closed.
- `internal/appdir` — resolves the OS-appropriate XDG-style data directory for the SQLite file
  (`$XDG_DATA_HOME/goread` on Linux, `~/Library/Application Support/goread` on macOS,
  `%LOCALAPPDATA%\goread` on Windows), creating it if needed. Override the DB path in tests/dev via the
  `GOREAD_DB_PATH` env var (read directly in `main.go`).
- `internal/db` — all persistence:
  - `db.go`: opens the SQLite connection with `SetMaxOpenConns(1)` and applies required PRAGMAs
    (`foreign_keys`, `busy_timeout`, `journal_mode=WAL`, `synchronous=NORMAL`) once on that single connection.
    **Do not raise `MaxOpenConns`** — several PRAGMAs are per-connection, so a pool of >1 connection would
    silently lose them on new connections, and SQLite only supports one writer at a time anyway.
  - `migrate.go`: embeds `migrations/*.sql` via `go:embed` and applies any not yet recorded in the
    `schema_migrations` table, in ascending numeric-prefix order (`0001_init.sql`, `0002_...sql`, ...). Add new
    schema changes as new numbered files here — never edit an already-applied migration file. Each migration
    file is executed as a single multi-statement `Exec` inside a transaction, so a migration can freely mix
    DDL, triggers, and data changes.
  - `0002_search_favicon.sql` adds `feeds.favicon_content_type` and the `articles_fts` FTS5 virtual table
    (title/author/content_text/feed_title) used by search. It's a regular (non-external-content) FTS5 table, kept
    in sync with plain `INSERT`/`UPDATE`/`DELETE` triggers on `articles` and a `feeds` title-update trigger —
    **not** FTS5's special `'delete'` command, because that requires reproducing a row's exact original indexed
    values, which isn't available once a feed row is gone (e.g. mid-cascade-delete of its articles).
  - `feed_repo.go` / `article_repo.go`: repository layer (CRUD) over the `feeds`/`articles` tables. Both tables
    are `STRICT`. `ArticleRepo.FindByIdentity` implements the feed-identity fallback chain from the spec
    (GUID → Link → content hash) — this is the core dedup/sync mechanism relied on by `feed.Syncer`.
    `ArticleRepo.Search(query, limit)` queries `articles_fts` and joins back to `articles`/`feeds`;
    `buildFTSQuery` turns free-form user input into a safe, quoted, ANDed prefix-match expression so arbitrary
    input can never be interpreted as FTS5 query syntax. `articleSelectColumns` qualifies every column with
    `articles.` (needed once `Search`'s join with `feeds` makes bare column names ambiguous) — keep using it
    rather than reintroducing unqualified `SELECT id, ...` on new queries.
  - `backup.go`: `Backup(sqlDB, w)` streams a consistent snapshot to w via `VACUUM INTO` a temp file (then
    copies and removes it), rather than copying the on-disk `.sqlite` file directly — the latter would race the
    WAL journal (`journal_mode=WAL`, see pragmas above) and could omit committed-but-not-checkpointed data.
    Backs `GET /backup` in `internal/server`.
- `internal/model` — plain structs (`Feed`, `Article`, `SearchResult`) shared across the DB layer and the web
  layer.
- `internal/browser` — `Open(url)` shells out to the OS's default-browser opener (`xdg-open`/`open`/`rundll32`)
  at startup, per spec ("Open the browser"). `main.go` calls it in its own goroutine so a slow or failing opener
  can never block server startup or feed refreshing.
- `internal/server` — HTTP server and the server-rendered web UI:
  - `listen.go`: `Listen(startPort)` binds to `localhost` starting at the given port, incrementing on
    `EADDRINUSE` until a free port is found (per spec, GoRead must never fail to start due to a busy port 8080).
  - `templates.go`: `go:embed`s `templates/*.html` and `static/*` (CSS + vendored `htmx.min.js`) into the
    binary — no external files are read at runtime, keeping `go install` self-contained.
  - `templates/`: `layout.html` defines the three-pane grid (`{{template "feed_tree"}}` /
    `"article_list"` / `"article_content"}}`), each pane its own template file so `handler.go` can reuse the
    same `pageData` across full-page and HTMX-fragment routes. `hx-boost="true"` on the outer `#app` div turns
    every link, and every plain or multipart `<form>`, inside it into an AJAX request that swaps `#app` — new
    interactive UI (search box, OPML import form, refresh/read-state buttons) gets this for free without extra
    `hx-*` attributes as long as it's a normal link/form/button inside `#app`.
  - `article_content.html` embeds an `<iframe sandbox="">` pointing at `/feeds/{id}/articles/{id}/content`,
    which is rendered separately (`handleArticleFrame`) with its own, even-stricter `frameCSP` — the sanitized
    article HTML is never inlined into the main page's DOM.
  - `handler.go` — `Handler.Routes()` wires the full page/fragment routes (`GET /`, `/feeds/{id}`,
    `/feeds/{id}/articles/{articleID}`), read-state and manual-refresh actions, and:
    - `GET /search` — full-text search via `ArticleRepo.Search`; results render in the article-list pane
      (`pageData.SearchActive`/`SearchResults`) in place of a single feed's article list, since a search result
      set spans multiple feeds.
    - `GET /feeds/{id}/favicon` — serves the stored favicon blob (never fetches live; see `internal/feed`
      below for when it's populated).
    - `GET /opml/export` / `POST /opml/import` — OPML download/upload, delegating to `internal/opml` and
      `feed.ImportOPML`.
    - `GET /proxy/image` — routes remote article images through `internal/imageproxy` so the browser never
      contacts a third-party host directly; responses set `Cache-Control: no-store` (images are re-fetched on
      every view, per spec).
    - `GET /backup` — forces download of a `.sqlite` snapshot via `db.Backup`, per spec ("Backups").
    Since `#app`'s `hx-boost="true"` (see below) would otherwise intercept these two file-download links as
    AJAX navigations and try to swap the raw file bytes into the page, both the OPML export and backup links in
    `feed_tree.html` carry `hx-boost="false"` so they fall through to a normal browser navigation/download
    instead.
    All routes share `render()`, which always reloads the feed list to rebuild the left-pane tree and picks the
    `"layout"` (full page) vs `"app"` (fragment) template based on the `HX-Request` header. `buildFolders`
    groups the already-`ORDER BY folder, title`-sorted feed list into consecutive buckets, labeling the empty
    folder as "Uncategorized".
- `internal/sanitize` — strips article HTML to a safe subset before it's rendered in the sandboxed iframe, via
  `bluemonday.UGCPolicy()` plus a small allow-list (images, basic table attrs, nofollow/noreferrer/target=_blank
  links). `RewriteImageSrcs` walks the sanitized HTML (using `golang.org/x/net/html`) to point every `<img src>`
  at the local image proxy and strips `srcset`/similar attributes that could otherwise let the browser bypass it.
- `internal/imageproxy` — fetches a single remote image per request on the server's behalf (never cached to
  disk). `checkHost` resolves the target and rejects loopback/private/link-local/multicast addresses so
  untrusted feed content can't use the proxy as an SSRF pivot against the local network; `NewForTesting()`
  disables that gate so tests can point it at an `httptest` server bound to `127.0.0.1`. `internal/favicon` uses
  the identical gating pattern independently (duplicated rather than shared, since the two packages fetch
  different things for different reasons).
- `internal/feed` — feed parsing, HTTP fetching, database synchronization, scheduling, favicons, and OPML
  import:
  - `parse.go`: `Parse([]byte) (*gofeed.Feed, error)` wraps `gofeed.Parser` (with `KeepOriginalFeed: true`, so
    `ttl.go` can reach the underlying `*rss.Feed`), which auto-detects RSS 0.9x/1.0/2.0 and Atom from the
    document itself. This package works on already-fetched bytes; it does not perform HTTP requests itself.
  - `identity.go`: `ContentHash(link, date)` implements the third link of the spec's article identity chain
    (GUID → Link → content hash); the first two are matched directly against stored columns.
  - `convert.go`: maps a `gofeed.Item` into a `model.Article` (author resolution across `Author`/`Authors`,
    HTML-to-text stripping for `ContentText`, JSON-encoded `Metadata` for enclosures/categories).
  - `sync.go`: `Syncer.Sync(feedID, parsed)` is the core reconciliation loop — it refreshes the feed's own
    title/description/site URL, then for each parsed item resolves identity via
    `ArticleRepo.FindByIdentity` to decide insert vs. update (updates preserve the existing row's ID and read
    state), and finally marks any previously-stored article not present in this sync as `model.ArticleStateDeleted`
    (never hard-deleted) — including resurrecting one back to `ArticleStatePresent` if it reappears in a later
    sync.
  - `fetch.go`: `Fetcher.Fetch(ctx, feedURL, etag, lastModified)` performs the actual HTTP GET, sending
    `If-None-Match`/`If-Modified-Since` when cache values are known. It follows redirects manually (rather than
    via `http.Client`'s default behavior) so it can distinguish permanent (301/308) from temporary redirects —
    only permanent ones are reported back via `FetchResult.FinalURL` for the caller to persist as the feed's new
    URL, per spec.
  - `ttl.go`: `TTL(*gofeed.Feed) time.Duration` resolves a feed's refresh interval — RSS's `<ttl>` element, then
    the RSS/Atom Syndication extension (`sy:updatePeriod`/`sy:updateFrequency`), then `DefaultTTL` (60m) — always
    floored at `MinTTL` (5m) so a misconfigured feed can't force excessive refreshing.
  - `refresh.go`: `Refresher.Refresh(ctx, feedID)` is one full refresh cycle: conditional fetch → (on 304, just
    update timestamps) → parse → `Syncer.Sync` → `fetchFaviconIfMissing`, recording `ETag`/`LastModified`/
    `RefreshTTL` on success. Fetch and parse failures are recorded as `Feed.RefreshError` and leave existing
    data untouched rather than being returned as a Go error — a returned error means the feed itself couldn't be
    loaded/persisted. `fetchFaviconIfMissing` only runs (and only fetches) once per feed — it's a no-op once
    `Feed.Favicon` is non-empty — trying the parsed feed's `Image.URL` (Atom `<icon>`/RSS `<image>`, per gofeed's
    unified mapping) then falling back to `SiteURL + "/favicon.ico"` via `internal/favicon`; failures are logged
    and never fail the refresh. `Refresher.Favicon` is `nil`-able — tests that don't want real network calls set
    it to `nil` (see `newTestRefresher` in `refresh_test.go`) rather than using the `NewRefresher` default.
    `Due(feed, now)` decides whether a feed's `LastRefreshAt`/`RefreshTTL` mean it's due again.
  - `scheduler.go`: `Scheduler.Run(ctx)` is the background worker — it refreshes due feeds sequentially (one at a
    time, per spec) on a `SchedulerInterval` tick, plus a `TriggerRefresh(feedID)` channel for manual/OPML-
    triggered refreshes and a `TriggerRefreshSync(ctx, feedID)` variant that blocks until the scheduler has
    processed it (used by the UI's manual "refresh now" button). Once a single feed's `Refresh` has started it
    always runs to completion against `context.Background()`, not `ctx` — `ctx` is only checked between feeds,
    so cancelling it (graceful shutdown) never aborts an in-flight fetch, only stops new ones from starting.
  - `opml_import.go`: `ImportOPML(feeds, scheduler, r)` parses an OPML document (`internal/opml`) and, per feed
    URL, creates a new feed or updates an existing one's title/folder/site URL, then queues each through
    `scheduler.TriggerRefresh` for an immediate background refresh per spec ("refresh imported feeds
    immediately") — `scheduler` may be `nil` to skip that step.
- `internal/opml` — pure parsing/generation of OPML 2.0 documents, with no DB dependency (`Parse`/`Generate`
  operate on `[]opml.Feed`, not `model.Feed`, so callers translate). Nested `<outline>` folder groups are
  flattened into a single `Feed.Folder` string joined with `/`, since GoRead's schema stores folder as one flat
  column rather than a true hierarchy; round-tripping through `Generate` preserves that flattened string as a
  single-level outline group, not a re-nested hierarchy. `unwrapFeedOutline` detects and collapses Thunderbird's
  export quirk of wrapping every single feed in its own intermediate outline named (almost) identically to the
  feed itself, which would otherwise produce a redundant `folder/podcastname/podcastname` nesting — it only
  collapses a single-child wrapper whose name matches (or loosely relates to, allowing for punctuation drift or
  a shortened form) its child's, via `normalizeName`, so a genuine folder that happens to contain just one
  differently-named feed is left alone.
- `internal/favicon` — fetches and downsizes a feed's favicon for storage (`Client.Fetch(ctx, candidateURL,
  siteURL)`), trying `candidateURL` first and `siteURL + "/favicon.ico"` second; returns a `nil` `*Result` with
  a `nil` error (not an error) when neither source yields anything, since a missing favicon isn't a failure.
  Decodable images (PNG/JPEG/GIF — anything `image.Decode` handles) are resized to `targetSize` (32px on the
  longest edge) and re-encoded as PNG; anything undecodable (notably classic multi-image `.ico` favicons) is
  stored exactly as fetched, since browsers render `.ico` fine via `<img src>` and the goal is bounding storage
  size, not universal re-encoding.

### Key implementation details worth knowing

- Timestamps are stored as `TEXT` in RFC3339Nano via `timeToNullString`/`nullStringToTime` helpers in
  `feed_repo.go`, not as SQLite's native datetime — keep new time-valued columns consistent with this.
- Articles are never hard-deleted when they disappear from a feed; `model.ArticleState` (`present`/`deleted`)
  tracks this, and `feed.Syncer.Sync` (see above) is what flips it.
- `internal/db` tests spin up a real temp-file SQLite DB per test via `newTestDB(t)` (in
  `feed_repo_test.go`) rather than mocking — keep doing this for new DB-layer tests, it's what exercises the
  PRAGMAs/FK constraints/STRICT tables/FTS5 triggers for real. `internal/feed`'s and `internal/server`'s tests
  follow the same pattern using the exported `db.Open`.
- Tests that exercise `feed.Refresher` construct it via `NewRefresher` and then explicitly set `.Favicon = nil`
  (see `newTestRefresher`) to avoid making real outbound HTTP requests during `go test`; only
  `internal/favicon`'s own tests (and one dedicated `TestRefresher_Refresh_FetchesFaviconOnce` using
  `favicon.NewForTesting()` against an `httptest` server) exercise the real fetch path.
- `internal/imageproxy` and `internal/favicon` each implement their own private/loopback network gate
  (`checkHost`/`isBlockedIP`) with a `NewForTesting()` constructor that disables it — use that constructor, not
  the real `New()`, whenever a test needs to point either client at an `httptest` server.
