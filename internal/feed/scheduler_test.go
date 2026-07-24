package feed

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tiim/goread/internal/db"
	"github.com/Tiim/goread/internal/model"
)

func TestScheduler_Run_RefreshesDueFeedsAndStopsOnCancel(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Write([]byte(refreshFeedXML))
	}))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "test.sqlite")
	sqlDB, err := db.Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer sqlDB.Close()

	feeds := db.NewFeedRepo(sqlDB)
	articles := db.NewArticleRepo(sqlDB)
	f := &model.Feed{FeedURL: srv.URL}
	if err := feeds.Create(f); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	refresher := NewRefresher(feeds, articles)
	sched := NewScheduler(feeds, refresher, time.Hour) // long interval; rely on the initial refreshDue pass

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		sched.Run(ctx)
		close(done)
	}()

	deadline := time.After(2 * time.Second)
	for hits == 0 {
		select {
		case <-deadline:
			t.Fatal("scheduler never refreshed the due feed")
		case <-time.After(10 * time.Millisecond):
		}
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run() did not return after context cancellation")
	}
}

func TestScheduler_TriggerRefresh_RunsOutOfCycle(t *testing.T) {
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(refreshFeedXML))
		select {
		case <-done:
		default:
			close(done)
		}
	}))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "test.sqlite")
	sqlDB, err := db.Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer sqlDB.Close()

	feeds := db.NewFeedRepo(sqlDB)
	articles := db.NewArticleRepo(sqlDB)
	f := &model.Feed{FeedURL: srv.URL}
	if err := feeds.Create(f); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	now := time.Now()
	f.LastRefreshAt = &now // not due on its own; only the manual trigger should refresh it
	if err := feeds.Update(f); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	refresher := NewRefresher(feeds, articles)
	sched := NewScheduler(feeds, refresher, time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sched.Run(ctx)

	sched.TriggerRefresh(f.ID)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("manually triggered refresh never ran")
	}
}

func TestScheduler_TriggerRefreshSync_BlocksUntilComplete(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(refreshFeedXML))
	}))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "test.sqlite")
	sqlDB, err := db.Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer sqlDB.Close()

	feeds := db.NewFeedRepo(sqlDB)
	articles := db.NewArticleRepo(sqlDB)
	f := &model.Feed{FeedURL: srv.URL}
	if err := feeds.Create(f); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	now := time.Now()
	f.LastRefreshAt = &now // not due on its own; only the sync trigger should refresh it
	if err := feeds.Update(f); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	refresher := NewRefresher(feeds, articles)
	sched := NewScheduler(feeds, refresher, time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sched.Run(ctx)

	sched.TriggerRefreshSync(context.Background(), f.ID)

	got, err := feeds.Get(f.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.LastSuccessAt == nil {
		t.Fatal("expected feed to be refreshed by the time TriggerRefreshSync returns")
	}
}
