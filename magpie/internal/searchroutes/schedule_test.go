package searchroutes

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/searchdet"
)

// waitFor waits for the scheduler to finish the run it started, by its
// state, not by a fixed sleep.
func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("%s: still not true after 5s", what)
}

// TestSchedulerRunsDueSearchesAndPauses: a saved search runs through the
// runner on its interval, records the run's own result, and stops when
// paused and when cancelled.
func TestSchedulerRunsDueSearchesAndPauses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "search_schedules.json")
	var calls int32
	ran := make(chan struct{}, 8)
	run := func(ctx context.Context, opt searchdet.Options) searchdet.Investigation {
		atomic.AddInt32(&calls, 1)
		ran <- struct{}{}
		return searchdet.Investigation{
			TraceID: "trc_sched", Verdict: "VERIFIED", TotalLatencyMs: 12,
			Citations: []searchdet.Citation{{URL: "https://e.example/x"}},
		}
	}
	s := NewScheduler(path, run)
	base := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return base }

	sch, err := s.Add(Schedule{Query: "wiz", EverySec: 60, ModeID: "M11"})
	if err != nil {
		t.Fatal(err)
	}
	// not due yet: Add schedules the first run one interval from now
	s.runDue(context.Background(), base)
	if n := atomic.LoadInt32(&calls); n != 0 {
		t.Fatalf("ran %d times before its interval", n)
	}
	s.runDue(context.Background(), base.Add(61*time.Second))
	select {
	case <-ran:
	case <-time.After(5 * time.Second):
		t.Fatal("the due search never ran")
	}
	waitFor(t, "the run recorded", func() bool {
		list := s.List()
		return len(list) == 1 && list[0].Runs == 1
	})
	got := s.List()[0]
	if got.LastTraceID != "trc_sched" || got.LastVerdict != "VERIFIED" || got.LastLatencyMs != 12 || got.LastHits != 1 {
		t.Errorf("what the run came to: %+v", got)
	}
	if _, err := time.Parse(time.RFC3339, got.LastRunAt); err != nil {
		t.Errorf("LastRunAt %q: %v", got.LastRunAt, err)
	}

	if err := s.Pause(sch.ID); err != nil {
		t.Fatal(err)
	}
	if p := s.List()[0]; !p.Paused || p.NextRunAt != "" {
		t.Errorf("paused: %+v", p)
	}
	s.runDue(context.Background(), base.Add(600*time.Second))
	select {
	case <-ran:
		t.Fatal("a paused search ran")
	case <-time.After(30 * time.Millisecond):
	}

	if err := s.Resume(sch.ID); err != nil {
		t.Fatal(err)
	}
	s.runDue(context.Background(), base.Add(700*time.Second))
	select {
	case <-ran:
	case <-time.After(5 * time.Second):
		t.Fatal("the resumed search never ran")
	}
	waitFor(t, "the second run recorded", func() bool { return s.List()[0].Runs == 2 })

	// the file is the durable copy: a second scheduler reads the same runs
	again := NewScheduler(path, run)
	if err := again.Load(); err != nil {
		t.Fatal(err)
	}
	if list := again.List(); len(list) != 1 || list[0].Runs != 2 || list[0].Query != "wiz" {
		t.Errorf("after a reload: %+v", list)
	}

	if err := s.Cancel(sch.ID); err != nil {
		t.Fatal(err)
	}
	if list := s.List(); len(list) != 0 {
		t.Errorf("after cancel: %+v", list)
	}
	third := NewScheduler(path, run)
	if err := third.Load(); err != nil {
		t.Fatal(err)
	}
	if list := third.List(); len(list) != 0 {
		t.Errorf("a cancelled search is still saved: %+v", list)
	}
}

// TestSchedulerRunNowAndRefusals: a manual run is real and recorded too,
// and a bad interval or an unknown id is refused.
func TestSchedulerRunNowAndRefusals(t *testing.T) {
	path := filepath.Join(t.TempDir(), "search_schedules.json")
	run := func(ctx context.Context, opt searchdet.Options) searchdet.Investigation {
		return searchdet.Investigation{TraceID: "trc_now", Verdict: "ABSTAIN"}
	}
	s := NewScheduler(path, run)
	s.now = func() time.Time { return time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC) }
	if _, err := s.Add(Schedule{Query: "", EverySec: 60}); err == nil {
		t.Error("a search with no query was saved")
	}
	if _, err := s.Add(Schedule{Query: "q", EverySec: 0}); err == nil {
		t.Error("a search with no interval was saved")
	}
	sch, err := s.Add(Schedule{Query: "q", EverySec: 60})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunNow(context.Background(), "nope"); err == nil {
		t.Error("running an unknown schedule succeeded")
	}
	if _, err := s.RunNow(context.Background(), sch.ID); err != nil {
		t.Fatal(err)
	}
	if got := s.List()[0]; got.Runs != 1 || got.LastVerdict != "ABSTAIN" {
		t.Errorf("the manual run: %+v", got)
	}
}

// TestSchedulerUnreadableFileIsAnError: a failed read is unknown, never
// "no schedules", and what was loaded before stays.
func TestSchedulerUnreadableFileIsAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "search_schedules.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewScheduler(path, func(context.Context, searchdet.Options) searchdet.Investigation {
		return searchdet.Investigation{}
	})
	if err := s.Load(); err == nil {
		t.Fatal("a broken file loaded as no schedules")
	}
}
