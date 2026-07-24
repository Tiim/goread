package feed

import (
	"context"
	"log"
	"time"

	"github.com/Tiim/goread/internal/db"
)

// SchedulerInterval is how often the Scheduler checks all feeds for ones due
// a refresh.
const SchedulerInterval = time.Minute

// Scheduler periodically walks all feeds and refreshes any that are due,
// running refreshes sequentially in a single background goroutine so at
// most one feed is ever being fetched at a time, per spec. It continues
// past individual feed failures.
type Scheduler struct {
	Feeds     *db.FeedRepo
	Refresher *Refresher
	Interval  time.Duration
	Now       func() time.Time

	manual chan refreshRequest
}

// refreshRequest is a manually queued refresh. done, if non-nil, is closed
// once the scheduler's single refresh goroutine has processed it, letting a
// caller block until it's actually run without breaking the invariant that
// refreshes happen one at a time.
type refreshRequest struct {
	feedID int64
	done   chan struct{}
}

// NewScheduler creates a Scheduler that checks for due feeds every interval.
func NewScheduler(feeds *db.FeedRepo, refresher *Refresher, interval time.Duration) *Scheduler {
	return &Scheduler{
		Feeds:     feeds,
		Refresher: refresher,
		Interval:  interval,
		Now:       time.Now,
		manual:    make(chan refreshRequest, 16),
	}
}

// TriggerRefresh queues an immediate, out-of-cycle refresh for a single feed
// (used by OPML import), without blocking the caller. It is a no-op if the
// queue is full.
func (s *Scheduler) TriggerRefresh(feedID int64) {
	select {
	case s.manual <- refreshRequest{feedID: feedID}:
	default:
	}
}

// TriggerRefreshSync queues an immediate, out-of-cycle refresh for a single
// feed (used by the UI's manual "refresh now" button) and blocks until the
// scheduler has run it, so the caller can render the feed's post-refresh
// state. It still goes through the same queue as TriggerRefresh, so it never
// runs concurrently with a due-feed refresh. Returns early if ctx is
// canceled before the refresh is queued or completes.
func (s *Scheduler) TriggerRefreshSync(ctx context.Context, feedID int64) {
	req := refreshRequest{feedID: feedID, done: make(chan struct{})}
	select {
	case s.manual <- req:
	case <-ctx.Done():
		return
	}
	select {
	case <-req.done:
	case <-ctx.Done():
	}
}

// Run blocks, refreshing due feeds sequentially until ctx is canceled.
//
// A refresh that has already started always runs to completion, using a
// background context rather than ctx, so shutdown never aborts a fetch
// mid-flight (per spec: "finish any active feed refresh"). ctx is only
// consulted to decide whether to start another one.
func (s *Scheduler) Run(ctx context.Context) {
	ticker := time.NewTicker(s.Interval)
	defer ticker.Stop()

	s.refreshDue(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case req := <-s.manual:
			s.refreshOne(req.feedID)
			if req.done != nil {
				close(req.done)
			}
		case <-ticker.C:
			s.refreshDue(ctx)
		}
	}
}

func (s *Scheduler) refreshDue(ctx context.Context) {
	feeds, err := s.Feeds.List()
	if err != nil {
		log.Printf("scheduler: list feeds: %v", err)
		return
	}
	now := s.Now()
	for _, f := range feeds {
		if ctx.Err() != nil {
			return
		}
		if !Due(f, now) {
			continue
		}
		s.refreshOne(f.ID)
	}
}

func (s *Scheduler) refreshOne(feedID int64) {
	if _, err := s.Refresher.Refresh(context.Background(), feedID); err != nil {
		log.Printf("scheduler: refresh feed %d: %v", feedID, err)
	}
}
