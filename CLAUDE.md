# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project overview

GoRead is a lightweight, self-hosted RSS/Atom reader written in Go, for a single local user. It is distributed
as a single executable installable via `go install`, uses SQLite as its only datastore, requires no
authentication, and runs entirely on `localhost`. Full requirements are in `docs/spec.md`; the staged
implementation plan is in `docs/phases.md` — check which phase is current before starting work, and implement
phases in order.

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
why the SQLite driver is the pure-Go `modernc.org/sqlite`, not `mattn/go-sqlite3`, and why feed parsing uses the
pure-Go `github.com/mmcdole/gofeed` rather than a cgo-based XML/parsing library.

## Architecture

- `cmd/goread/main.go` — entrypoint. Wires together `appdir` (DB location) → `db.Open` (DB init/migration) →
  `server.Listen` (port binding) → `http.Serve`.
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
    schema changes as new numbered files here — never edit an already-applied migration file.
  - `feed_repo.go` / `article_repo.go`: repository layer (CRUD) over the `feeds`/`articles` tables. Both tables
    are `STRICT`. `ArticleRepo.FindByIdentity` implements the feed-identity fallback chain from the spec
    (GUID → Link → content hash) — this is the core dedup/sync mechanism and will be relied on by the Phase 2
    feed-sync logic.
- `internal/model` — plain structs (`Feed`, `Article`) shared across the DB layer and (eventually) the web layer.
- `internal/server` — `Listen(startPort)` binds to `localhost` starting at the given port, incrementing on
  `EADDRINUSE` until a free port is found (per spec, GoRead must never fail to start due to a busy port 8080).
- `internal/feed` — feed parsing and database synchronization (Phase 2):
  - `parse.go`: `Parse([]byte) (*gofeed.Feed, error)` wraps `gofeed.Parser`, which auto-detects RSS 0.9x/1.0/2.0
    and Atom from the document itself. This package works on already-fetched bytes; it does not perform HTTP
    requests itself — actual fetching, HTTP caching (`ETag`/`Last-Modified`), TTL enforcement, and redirect
    handling are Phase 3 concerns layered on top.
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

### Key implementation details worth knowing

- Timestamps are stored as `TEXT` in RFC3339Nano via `timeToNullString`/`nullStringToTime` helpers in
  `feed_repo.go`, not as SQLite's native datetime — keep new time-valued columns consistent with this.
- Articles are never hard-deleted when they disappear from a feed; `model.ArticleState` (`present`/`deleted`)
  tracks this, and `feed.Syncer.Sync` (see above) is what flips it.
- `internal/db` tests spin up a real temp-file SQLite DB per test via `newTestDB(t)` (in
  `feed_repo_test.go`) rather than mocking — keep doing this for new DB-layer tests, it's what exercises the
  PRAGMAs/FK constraints/STRICT tables for real. `internal/feed`'s sync tests follow the same pattern using the
  exported `db.Open`.
