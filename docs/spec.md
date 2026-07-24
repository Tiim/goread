# GoRead RSS Reader Requirements Specification

## Overview

**GoRead** is a lightweight, self-hosted RSS reader written in Go.

GoRead is intended for a single local user and is distributed as a single executable that can be installed using `go install`. It requires no external services other than a local SQLite database.

This document defines the requirements for version 1.0 (MVP).
## Goals

The application shall:

- Aggregate RSS and Atom feeds.
- Store complete feed content locally in a SQLite database.
- Allow offline reading after feeds have been synchronized.
- Render all UI on the local server and display it on the browser.
- Execute as a single Go binary.
- Require no authentication.
- Run entirely on localhost.
## Installation

GoRead shall be installable using:

```bash
go install
```

This means no external build system can be used. The application must be fully built using `go build cmd/goread/main.go`. 
## Startup

When GoRead starts it shall:

1. Locate or create the SQLite database.
2. Perform any required schema migrations.
3. Start the HTTP server.
4. Open the browser.
5. Begin refreshing feeds in the background.

The UI shall become available immediately.

Feed refreshes shall **not** block startup.

## HTTP Server

GoRead shall:

- bind to `localhost`
- attempt to listen on port `8080`
- if the port is unavailable, increment the port number until an available port is found
## Database

SQLite shall be used as the only database.
The database should be configured using best practices:
- PRAGMA foreign_keys = ON;
- PRAGMA busy_timeout = 5000;
- PRAGMA journal_mode = WAL;
- PRAGMA synchronous = NORMAL;
- Strict Tables
## Stored Data
Media files shall **not** be stored.
The database shall store:
### Feeds
- id
- title
- description
- feed URL
- site URL
- favicon
- refresh TTL
- HTTP cache metadata
    - ETag
    - Last-Modified
- folder
- last refresh timestamp
- last successful refresh
- refresh error state

### Articles
- id
- feed_id
- GUID
- title
- author
- publication date
- updated date
- link
- summary
- full feed content
- full feed text content
- content type
- read state
- feed state (present, deleted)
- metadata required by the feed

## Feed Support
Supported formats:
- RSS
- Atom
## Feed Identity

Articles shall be identified using:

1. GUID
2. Link
3. Content hash (Link + Date)

During synchronization:
- existing articles shall be updated
- articles missing from the feed shall remain in the database indefinitely
## Refresh

Feed refreshes shall:
- run sequentially
- respect HTTP cache headers
- respect feed TTL
- continue when individual feeds fail

A failed refresh shall:
- leave existing data unchanged
- mark the feed as unavailable
- display an error indicator in the UI

Permanent redirect responses shall be persisted

## Manual Refresh

Users shall be able to refresh an individual feed manually.

## OPML

The application shall support:

- OPML import
- OPML export

Import shall:

- preserve folder hierarchy
- update metadata for existing feeds
- refresh imported feeds immediately

## HTML Rendering

Article HTML shall be rendered safely.

Requirements:

- sanitize all HTML
- remove JavaScript
- remove inline event handlers
- remove forms
- remove unsafe elements
- render inside a sandboxed iframe

The iframe shall use a restrictive sandbox configuration.

A strict Content Security Policy (CSP) shall be applied.
## Images

Images shall not be stored.

Remote images shall be fetched through an application proxy every time they are viewed.

The proxy shall:

- prevent direct browser access to remote sites
- not cache images permanently

## Favicons

Favicons shall be obtained using the following priority:
1. Atom `<icon>`
2. RSS `<image>`
3. `/favicon.ico`

Favicons shall be stored as downsized images in the database.

## User Interface

The UI shall be fully server-side rendered.

Go's `html/template` package shall be used.

HTMX may be used for partial page updates.

No client-side application framework shall be used.
## Layout

The application shall use a three-pane layout.

Left:
- folders/feed tree

Middle:
- article list
Right:
- article content

## Accessibility

The application shall support standard keyboard accessibility.

## Search

SQLite FTS5 shall provide full-text search.

Search shall include:

- title
- author
- feed name
- article text content
## Read State

Each article shall maintain a read/unread state.
The UI shall support:

- mark article as read
- mark article as unread
- mark all articles in a feed as read
## Offline Behavior

After synchronization:

- all article content shall remain available offline
- media shall not

## Security

The application shall:

- bind only to localhost
- use Content Security Policy
- sandbox article rendering
- sanitize all article HTML
- never execute JavaScript from feeds
## Logging

Logging shall be written to stdout/stderr only.
Persistent log files are not required.

---

## Graceful Shutdown

On shutdown the application shall:

- finish any active feed refresh
- close the database cleanly
- exit without corrupting data

## Backups

Users shall be able to back up the SQLite database by downloading a .sqlite file through the UI.

## Development

### Testing

Development shall follow Test-Driven Development (TDD) where practical.
The following must be thoroughly unit tested
- feed parsing
- synchronization
- OPML import/export
- database layer
- search
- HTML sanitization
- image proxy
- feed identity logic
