# GoRead

GoRead is a lightweight, self-hosted RSS/Atom reader written in Go, for a single local user. It's distributed
as a single executable, uses SQLite as its only datastore, requires no authentication, and runs entirely on
`localhost`.

## Features

- Aggregates RSS and Atom feeds, with offline reading after sync
- Server-rendered UI (no JS framework, just HTMX)
- Full-text search across all articles
- Folders for organizing feeds
- OPML import/export
- Automatic favicon fetching
- Feed merge detection (when a feed permanently redirects to another already-tracked feed)
- SQLite backups (`GET /backup`)
- No external services, no authentication, no CGO

## Install

```bash
go install github.com/Tiim/goread/cmd/goread@latest
```

This installs a `goread` binary to your `$GOBIN` (or `$GOPATH/bin`). Make sure that directory is on your `PATH`.

## Usage

```bash
goread
```

On first run, GoRead creates its SQLite database, starts an HTTP server on `localhost` (starting at port
`8080`, incrementing if that port is busy), and opens your default browser to the UI. Feed refreshing happens
automatically in the background.

The database location is OS-specific (XDG data dir on Linux, `~/Library/Application Support/goread` on macOS,
`%LOCALAPPDATA%\goread` on Windows). Override it with the `GOREAD_DB_PATH` environment variable.

## Development

```bash
go build ./...      # build everything
go vet ./...         # static checks
gofmt -l .            # list files needing formatting (should be empty)
go test ./...        # run all tests
go run ./cmd/goread   # run the server locally
```

See `docs/spec.md` for the full requirements specification and `docs/phases.md` for the implementation plan.
