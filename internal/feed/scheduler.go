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

	manual chan int64
}

// NewScheduler creates a Scheduler that checks for due feeds every interval.
func NewScheduler(feeds *db.FeedRepo, refresher *Refresher, interval time.Duration) *Scheduler {
	return &Scheduler{
		Feeds:     feeds,
		Refresher: refresher,
		Interval:  interval,
		Now:       time.Now,
		manual:    make(chan int64, 16),
	}
}

// TriggerRefresh queues an immediate, out-of-cycle refresh for a single feed
// (used by manual "refresh now" and OPML import), without blocking the
// caller. It is a no-op if the queue is full.
func (s *Scheduler) TriggerRefresh(feedID int64) {
	select {
	case s.manual <- feedID:
	default:
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
		case feedID := <-s.manual:
			s.refreshOne(feedID)
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
