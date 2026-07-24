package feed

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/Tiim/goread/internal/db"
	"github.com/Tiim/goread/internal/model"
)

const sampleAddFeedRSS = `<?xml version="1.0"?>
<rss version="2.0"><channel>
<title>Sample Feed</title>
<link>https://example.com</link>
<item><title>Item 1</title><guid>1</guid></item>
</channel></rss>
`

func newTestFeedRepo(t *testing.T) *db.FeedRepo {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.sqlite")
	sqlDB, err := db.Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	return db.NewFeedRepo(sqlDB)
}

func TestAddFeed_CreatesFeedFromParsedMetadata(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(sampleAddFeedRSS))
	}))
	defer srv.Close()

	feeds := newTestFeedRepo(t)

	f, err := AddFeed(context.Background(), NewFetcher(), feeds, nil, srv.URL, "Tech")
	if err != nil {
		t.Fatalf("AddFeed() error = %v", err)
	}
	if f.Title != "Sample Feed" || f.SiteURL != "https://example.com" || f.Folder != "Tech" {
		t.Errorf("AddFeed() feed = %+v, want Title=Sample Feed SiteURL=https://example.com Folder=Tech", f)
	}
	if f.ID == 0 {
		t.Errorf("AddFeed() did not assign an ID")
	}

	stored, err := feeds.Get(f.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if stored.FeedURL != srv.URL {
		t.Errorf("stored feed URL = %q, want %q", stored.FeedURL, srv.URL)
	}
}

func TestAddFeed_RejectsUnreachableURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	feeds := newTestFeedRepo(t)

	if _, err := AddFeed(context.Background(), NewFetcher(), feeds, nil, srv.URL, ""); err == nil {
		t.Fatal("AddFeed() error = nil, want error for 404 response")
	}

	all, err := feeds.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(all) != 0 {
		t.Errorf("List() = %d feeds, want 0 after a rejected add", len(all))
	}
}

func TestAddFeed_RejectsInvalidXML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not a feed"))
	}))
	defer srv.Close()

	feeds := newTestFeedRepo(t)

	if _, err := AddFeed(context.Background(), NewFetcher(), feeds, nil, srv.URL, ""); err == nil {
		t.Fatal("AddFeed() error = nil, want error for unparsable body")
	}
}

func TestAddFeed_RejectsDuplicateURL(t *testing.T) {
	feeds := newTestFeedRepo(t)
	existing := &model.Feed{Title: "Existing", FeedURL: "https://existing.example.com/feed.xml"}
	if err := feeds.Create(existing); err != nil {
		t.Fatalf("create existing feed: %v", err)
	}

	_, err := AddFeed(context.Background(), NewFetcher(), feeds, nil, existing.FeedURL, "")
	if !errors.Is(err, ErrFeedURLExists) {
		t.Fatalf("AddFeed() error = %v, want ErrFeedURLExists", err)
	}
}

func TestAddFeed_TriggersRefresh(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(sampleAddFeedRSS))
	}))
	defer srv.Close()

	feeds := newTestFeedRepo(t)
	scheduler := &Scheduler{manual: make(chan refreshRequest, 1)}

	f, err := AddFeed(context.Background(), NewFetcher(), feeds, scheduler, srv.URL, "")
	if err != nil {
		t.Fatalf("AddFeed() error = %v", err)
	}

	select {
	case req := <-scheduler.manual:
		if req.feedID != f.ID {
			t.Errorf("queued refresh for feed %d, want %d", req.feedID, f.ID)
		}
	default:
		t.Fatal("AddFeed() did not queue a refresh")
	}
}
