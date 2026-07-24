### Phase 1: Foundation & Data Layer

Goal: The application starts, dynamically binds to an available local port, initializes the SQLite database with best practices, and can safely store and retrieve raw data.

- Tasks:
    - Set up the Go module and basic project structure.
    - Implement the HTTP server startup logic (try port `8080`, increment until an available port on `localhost` is found).
    - Initialize SQLite with required PRAGMAs (Strict Tables, WAL, Foreign Keys, Normal Sync, Busy Timeout).
    - Create and apply database schema migrations for `Feeds` and `Articles` tables.
    - Implement the database repository layer (CRUD operations for feeds and articles).
    - TDD Focus: Thoroughly unit test the database layer (inserts, updates, foreign key constraints).

### Phase 2: Feed Ingestion & Identity Engine

Goal: The system can fetch, parse, and synchronize RSS and Atom feeds, applying strict identity rules to insert new articles or update existing ones without duplication.

- Tasks:
    - Implement XML parsers for RSS and Atom formats.
    - Implement the feed identity logic to identify articles uniquely (Fallback chain: GUID -> Link -> Content Hash).
    - Implement synchronization logic: update existing articles, insert new ones, and leave missing ones in the database indefinitely.
    - TDD Focus: Feed parsing (various valid/invalid XML structures), feed identity logic, and database synchronization logic.

### Phase 3: Background Processor & Graceful Shutdown

Goal: Feeds refresh asynchronously in the background, handle network errors gracefully, respect caching rules, and the application shuts down without data corruption.

- Tasks:
    - Build a background worker pool to execute feed refreshes sequentially.
    - Implement HTTP caching logic (parse and store `ETag` and `Last-Modified`, inject them into subsequent requests).
    - Implement feed TTL parsing and enforce refresh intervals.
    - Handle permanent HTTP redirects (update the feed URL in the DB).
    - Listen for OS interrupt signals (`SIGINT`, `SIGTERM`) to finish active refreshes, close the DB cleanly, and exit.
    - TDD Focus: Synchronization behavior (handling HTTP 304 Not Modified, errors, and TTL logic).

### Phase 4: Core Web Server & UI Layout

Goal: The server renders a basic, read-only HTML interface using Go's `html/template` that displays the folder/feed tree and article lists.

- Tasks:
    - Embed all HTML templates and CSS into the Go binary.
    - Create the three-pane CSS layout (Left: Folders/Feeds, Middle: Article List, Right: Article Content).
    - Implement server-side rendering for the feed tree and article list queries.
    - Ensure all logging outputs strictly to `stdout`/`stderr`.

### Phase 5: Secure Content Rendering & Image Proxy

Goal: Article content is securely displayed without executing scripts or exposing the user's IP to third parties.

- Tasks:
    - Implement HTML sanitization (remove `<script>`, forms, inline event handlers, and unsafe elements).
    - Create an application proxy endpoint to fetch and serve remote images.
    - Modify sanitized HTML to route all `<img>` `src` attributes through the local image proxy.
    - Configure a strict Content Security Policy (CSP) header.
    - Render the sanitized article HTML inside a sandboxed `iframe`.
    - TDD Focus: HTML sanitization edge-cases, Image Proxy behavior (no permanent caching, rejecting non-image payloads).

### Phase 6: Interactivity & State Management

Goal: The UI becomes fully interactive using HTMX for partial page reloads, allowing users to manage their reading state and manually trigger updates.

- Tasks:
    - Integrate HTMX to handle clicks on the feed tree and article list without full page reloads.
    - Implement read/unread state toggling for individual articles.
    - Implement the "mark all articles in a feed as read" functionality.
    - Add a manual refresh button for individual feeds and handle the UI error indicators if a feed fails.
    - Implement standard keyboard accessibility (tabbing through the three-pane layout).

### Phase 7: Search, OPML, and Media Enrichment

Goal: Users can import/export their feed subscriptions, perform full-text searches on articles, and see favicons in the UI.

- Tasks:
    - Implement SQLite FTS5 virtual tables and triggers to index article text, titles, authors, and feed names.
    - Build the search UI and backend endpoint.
    - Implement OPML import (preserve folder hierarchy, create/update feeds, trigger immediate background refresh) and export.
    - Implement the favicon fetcher (priority: Atom `<icon>` -> RSS `<image>` -> `/favicon.ico`), downsize them, and store them in the database as blobs.
    - TDD Focus: Search queries, OPML parsing and generation.

### Phase 8: Finalization, Polish & Browser Integration

Goal: The application behaves exactly as a seamless local desktop app and fulfills the final MVP deployment requirements.

- Tasks:
    - Add a startup trigger to automatically open the user's default web browser to the bound `localhost` URL (ensure this does not block startup).
    - Implement the database backup feature (an endpoint that forces the download of the `.sqlite` file).
    - Perform a full accessibility sweep.
    - Verify the build process (`go install cmd/goread/main.go`) pulls no external static files and results in a single, fully functional binary.

### Phase 9: Feed Merging

Goal: When two separately-added feeds turn out to point at the same underlying source (e.g. their permanent redirects converge on the same canonical URL — see `internal/feed/refresh.go`'s `GetByURL` collision guard, which currently just keeps each feed's original URL to avoid a `UNIQUE` constraint failure), the user can explicitly merge them into one instead of carrying two duplicate entries indefinitely.

- Tasks:
    - Detect and surface convergent-URL feed pairs to the user (e.g. a UI indicator/list) rather than silently leaving them split, building on the existing collision guard in `Refresher.Refresh`.
    - Design and implement a "merge feeds" action: user picks the surviving feed (title/folder), reassign the other feed's `articles.feed_id` rows to it, reconcile read states, and delete the losing feed row.
    - Decide how article identity/dedup interacts with merging (two previously-independent feeds may have separately-synced overlapping articles under different GUIDs).
    - TDD Focus: Article reassignment correctness, read-state preservation, and rollback safety if a merge fails partway through.
