package searchroutes

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/yetone/magpie/internal/searchdet"
)

// Schedule is one saved search that runs again on its own interval. Every
// field after EverySec is what the last real run came to; none is invented
// for a run that has not happened.
type Schedule struct {
	ID     string `json:"id"`
	Name   string `json:"name,omitempty"`
	Query  string `json:"query"`
	ModeID string `json:"modeId,omitempty"`
	// EverySec is the interval. One second is the floor.
	EverySec int `json:"everySec"`
	// Paused stops the next run; the schedule and its history stay.
	Paused        bool   `json:"paused"`
	CreatedAt     string `json:"createdAt"`
	LastRunAt     string `json:"lastRunAt,omitempty"`
	NextRunAt     string `json:"nextRunAt,omitempty"`
	Runs          int    `json:"runs"`
	LastTraceID   string `json:"lastTraceId,omitempty"`
	LastVerdict   string `json:"lastVerdict,omitempty"`
	LastLatencyMs int    `json:"lastLatencyMs,omitempty"`
	LastHits      int    `json:"lastHits,omitempty"`
	LastError     string `json:"lastError,omitempty"`
}

// Runner is what a due schedule runs: the process's engine.
type Runner func(ctx context.Context, opt searchdet.Options) searchdet.Investigation

// Scheduler runs saved searches on a real ticker and persists them. It owns
// the file; the runner is the caller's engine, so tests run a fake one and
// never touch the network.
type Scheduler struct {
	path string
	run  Runner
	now  func() time.Time
	tick time.Duration

	mu        sync.Mutex
	schedules map[string]*Schedule
	running   map[string]context.CancelFunc
	cancel    context.CancelFunc
	done      chan struct{}
	wg        sync.WaitGroup
}

// NewScheduler is a scheduler over path, running run. It has loaded
// nothing until Load.
func NewScheduler(path string, run Runner) *Scheduler {
	return &Scheduler{
		path: path, run: run, now: time.Now, tick: time.Second,
		schedules: map[string]*Schedule{}, running: map[string]context.CancelFunc{},
	}
}

type scheduleFile struct {
	Schedules []*Schedule `json:"schedules"`
}

// Load reads the saved schedules. A file that is not there is empty; one
// that cannot be read or parsed is an error and what was loaded stays.
func (s *Scheduler) Load() error {
	b, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var f scheduleFile
	if err := json.Unmarshal(b, &f); err != nil {
		return fmt.Errorf("%s doesn't parse: %w", s.path, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sch := range f.Schedules {
		if sch != nil && sch.ID != "" && sch.Query != "" {
			s.schedules[sch.ID] = sch
		}
	}
	return nil
}

// List is the saved schedules, by id.
func (s *Scheduler) List() []Schedule {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listLocked()
}

func (s *Scheduler) listLocked() []Schedule {
	out := make([]Schedule, 0, len(s.schedules))
	for _, sch := range s.schedules {
		out = append(out, *sch)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Add saves a schedule and returns it as stored. It runs on its interval
// from now, not at once: adding a search is not running it.
func (s *Scheduler) Add(in Schedule) (Schedule, error) {
	if trimSpace(in.Query) == "" {
		return Schedule{}, fmt.Errorf("a scheduled search needs a query")
	}
	if in.EverySec < 1 {
		return Schedule{}, fmt.Errorf("the interval must be at least 1 second")
	}
	now := s.now()
	sch := Schedule{
		ID: in.ID, Name: in.Name, Query: in.Query, ModeID: in.ModeID,
		EverySec: in.EverySec, CreatedAt: now.Format(time.RFC3339),
		NextRunAt: now.Add(time.Duration(in.EverySec) * time.Second).Format(time.RFC3339),
	}
	if sch.ID == "" {
		sch.ID = fmt.Sprintf("sch_%d", now.UnixNano())
	}
	if sch.Name == "" {
		sch.Name = firstRunes(sch.Query, 40)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.copyLocked()
	next[sch.ID] = &sch
	if err := s.write(next); err != nil {
		return Schedule{}, err
	}
	s.schedules = next
	return sch, nil
}

// Pause stops the next run and ends one in flight. The schedule stays.
func (s *Scheduler) Pause(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sch, ok := s.schedules[id]
	if !ok {
		return fmt.Errorf("no scheduled search %q", id)
	}
	next := s.copyLocked()
	cp := *sch
	cp.Paused, cp.NextRunAt = true, ""
	next[id] = &cp
	if err := s.write(next); err != nil {
		return err
	}
	s.schedules = next
	if cancel := s.running[id]; cancel != nil {
		cancel()
	}
	return nil
}

// Resume lets the next run happen, one interval from now.
func (s *Scheduler) Resume(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sch, ok := s.schedules[id]
	if !ok {
		return fmt.Errorf("no scheduled search %q", id)
	}
	next := s.copyLocked()
	cp := *sch
	cp.Paused = false
	cp.NextRunAt = s.now().Add(time.Duration(cp.EverySec) * time.Second).Format(time.RFC3339)
	next[id] = &cp
	if err := s.write(next); err != nil {
		return err
	}
	s.schedules = next
	return nil
}

// Cancel ends a schedule in flight and removes it.
func (s *Scheduler) Cancel(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.schedules[id]; !ok {
		return fmt.Errorf("no scheduled search %q", id)
	}
	next := s.copyLocked()
	delete(next, id)
	if err := s.write(next); err != nil {
		return err
	}
	s.schedules = next
	if cancel := s.running[id]; cancel != nil {
		cancel()
	}
	return nil
}

// RunNow runs one saved search now, whatever its interval says, and records
// the real result. It refuses a schedule already running.
func (s *Scheduler) RunNow(ctx context.Context, id string) (searchdet.Investigation, error) {
	s.mu.Lock()
	sch, ok := s.schedules[id]
	if !ok {
		s.mu.Unlock()
		return searchdet.Investigation{}, fmt.Errorf("no scheduled search %q", id)
	}
	if _, busy := s.running[id]; busy {
		s.mu.Unlock()
		return searchdet.Investigation{}, fmt.Errorf("%s is already running", id)
	}
	runCtx, cancel := context.WithCancel(ctx)
	s.running[id] = cancel
	cp := *sch
	s.mu.Unlock()
	defer func() {
		cancel()
		s.mu.Lock()
		delete(s.running, id)
		s.mu.Unlock()
	}()
	return s.execute(runCtx, cp), nil
}

// Start runs the ticker until ctx ends or Stop is called.
func (s *Scheduler) Start(ctx context.Context) {
	s.mu.Lock()
	if s.cancel != nil {
		s.mu.Unlock()
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	s.cancel, s.done = cancel, done
	s.mu.Unlock()
	go s.loop(runCtx, done)
}

// Stop ends the ticker and waits for the runs it started.
func (s *Scheduler) Stop() {
	s.mu.Lock()
	cancel, done := s.cancel, s.done
	s.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	if done != nil {
		<-done
	}
	s.wg.Wait()
	s.mu.Lock()
	s.cancel, s.done = nil, nil
	s.mu.Unlock()
}

func (s *Scheduler) loop(ctx context.Context, done chan struct{}) {
	defer close(done)
	t := time.NewTicker(s.tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			s.runDue(ctx, now)
		}
	}
}

// runDue starts every schedule whose NextRunAt has passed and is not paused
// or already running.
func (s *Scheduler) runDue(ctx context.Context, now time.Time) {
	s.mu.Lock()
	var due []Schedule
	for _, sch := range s.schedules {
		if sch.Paused || s.running[sch.ID] != nil {
			continue
		}
		next, ok := parseTime(sch.NextRunAt)
		if !ok || !next.After(now) {
			due = append(due, *sch)
		}
	}
	s.mu.Unlock()
	for _, sch := range due {
		sch := sch
		s.mu.Lock()
		if s.running[sch.ID] != nil {
			s.mu.Unlock()
			continue
		}
		runCtx, cancel := context.WithCancel(ctx)
		s.running[sch.ID] = cancel
		s.wg.Add(1)
		s.mu.Unlock()
		go func() {
			defer s.wg.Done()
			defer func() {
				cancel()
				s.mu.Lock()
				delete(s.running, sch.ID)
				s.mu.Unlock()
			}()
			s.execute(runCtx, sch)
		}()
	}
}

// execute runs one search through the runner and records what it came to.
func (s *Scheduler) execute(ctx context.Context, sch Schedule) searchdet.Investigation {
	rc, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	inv := s.run(rc, searchdet.Options{Query: sch.Query, ModeID: sch.ModeID, Origin: "Scheduled search"})
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.schedules[sch.ID]
	if !ok {
		return inv // cancelled while it ran: nothing to record it on
	}
	cp := *cur
	cp.LastRunAt = now.Format(time.RFC3339)
	cp.Runs++
	cp.LastTraceID, cp.LastVerdict = inv.TraceID, inv.Verdict
	cp.LastLatencyMs, cp.LastHits = inv.TotalLatencyMs, len(inv.Citations)
	cp.LastError = ""
	if ctx.Err() != nil {
		cp.LastError = ctx.Err().Error()
	}
	if !cp.Paused {
		cp.NextRunAt = now.Add(time.Duration(cp.EverySec) * time.Second).Format(time.RFC3339)
	}
	next := s.copyLocked()
	next[cp.ID] = &cp
	if err := s.write(next); err != nil {
		cp.LastError = err.Error()
		next[cp.ID] = &cp
	}
	s.schedules = next
	return inv
}

// NextRun is when sch is next due; the zero time for a paused one.
func NextRun(sch Schedule, now time.Time) time.Time {
	if sch.Paused {
		return time.Time{}
	}
	if t, ok := parseTime(sch.NextRunAt); ok {
		return t
	}
	if t, ok := parseTime(sch.LastRunAt); ok {
		return t.Add(time.Duration(sch.EverySec) * time.Second)
	}
	return now
}

func parseTime(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, s)
	return t, err == nil
}

func (s *Scheduler) copyLocked() map[string]*Schedule {
	out := make(map[string]*Schedule, len(s.schedules))
	for id, sch := range s.schedules {
		cp := *sch
		out[id] = &cp
	}
	return out
}

func (s *Scheduler) write(schedules map[string]*Schedule) error {
	f := scheduleFile{Schedules: make([]*Schedule, 0, len(schedules))}
	for _, sch := range schedules {
		f.Schedules = append(f.Schedules, sch)
	}
	sort.Slice(f.Schedules, func(i, j int) bool { return f.Schedules[i].ID < f.Schedules[j].ID })
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".search-schedules-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, s.path)
}

func firstRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
