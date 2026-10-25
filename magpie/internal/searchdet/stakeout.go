package searchdet

import (
	"context"
	"errors"
	"fmt"
	"log"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Stakeout is the watch half of the engine: a saved query is re-run on a
// schedule, each run's results are folded into a SimHash plus a URL list,
// and the next run is diffed against that snapshot. A check is a real
// search — the same channels an investigate call asks — so "nothing
// changed" means the live web answered the same way twice, not that a
// fixture said so.

const (
	// stakeoutTimeLayout is the stamp a job carries and the page shows:
	// local time, seconds resolution.
	stakeoutTimeLayout = "2006-01-02 15:04:05"

	// stakeoutSimHashCut is the near-duplicate cut from simhash.go's
	// nearDup (6 bits): a snapshot within it is the same page set, past it
	// is a real change.
	stakeoutSimHashCut = 6

	// stakeoutSnapshotCap caps what one snapshot keeps: 20 merged results
	// is what the stakeout card lists and what the URL snapshot stores.
	stakeoutSnapshotCap = 20

	// stakeoutTopURLs is how many URLs the diff hands back for display.
	stakeoutTopURLs = 10

	// stakeoutTick is how often the scheduler looks for due jobs. It is
	// shorter than every interval a job can have, so a job starts within
	// one tick of its time without the ticker deciding the schedule.
	stakeoutTick = 15 * time.Second
)

// StakeoutDiff is one real check's outcome: what the live web returned now,
// how far its SimHash is from the saved one, and which URLs appeared or
// vanished since.
type StakeoutDiff struct {
	JobID           string
	CheckedAt       string
	PrevHash        uint64
	NewHash         uint64
	HammingDistance int
	Changed         bool
	NewURLs         []string
	GoneURLs        []string
	NewCount        int
	Summary         string
	TopURLs         []string
}

// CheckStakeout runs the job's query against every enabled channel, folds
// what returns into a snapshot and diffs it against the job's saved one.
//
// The channels are the same set investigate uses (Tencent Cloud WSA when
// its keys are set, Tavily/Exa/Brave/SearXNG when theirs are, and the
// keyless aggregators DuckDuckGo, Bing, You.com, Wikipedia, Baidu Baike,
// GitHub, Bilibili and arXiv otherwise). It returns an error when no
// channel could answer — a check that found nothing because the network was
// down is not a snapshot, and reporting it as "stable" would be a lie.
//
// The returned job carries the new snapshot (SimHash, URL list, check time,
// badge and summary); the caller persists it.
func CheckStakeout(ctx context.Context, job StakeoutJob, k Keys) (StakeoutDiff, StakeoutJob, error) {
	now := time.Now()
	prevHash := job.SimHash
	prevURLs := parseSnapshotURLs(job.TopDomains)
	diff := StakeoutDiff{
		JobID:     job.ID,
		CheckedAt: now.Format(stakeoutTimeLayout),
		PrevHash:  prevHash,
	}

	// A watch is a search that runs unattended: it passes the same input
	// gate as an investigate call, so a red-line query is refused and the
	// query that leaves the machine is the masked one.
	maskedQuery, err := PrepareWatchQuery(job.Query)
	if err != nil {
		return diff, job, err
	}

	hash, urls, cites, err := snapshotRun(ctx, maskedQuery, k)
	if err != nil {
		return diff, job, err
	}
	for i := range urls {
		urls[i] = SanitizeText(urls[i])
	}

	diff.NewHash = hash
	diff.HammingDistance = hamming64(prevHash, hash)
	diff.NewURLs, diff.GoneURLs = diffURLs(prevURLs, urls)
	diff.NewCount = len(diff.NewURLs)
	diff.TopURLs = urls
	if len(diff.TopURLs) > stakeoutTopURLs {
		diff.TopURLs = diff.TopURLs[:stakeoutTopURLs]
	}

	// A job that was never checked has no snapshot to differ from: the
	// distance to a zero hash measures nothing, so the first check only
	// establishes the baseline. Every later check uses the SimHash cut.
	baseline := prevHash == 0 && len(prevURLs) == 0
	diff.Changed = !baseline && diff.HammingDistance > stakeoutSimHashCut

	switch {
	case baseline:
		diff.Summary = fmt.Sprintf("首次巡检建立基线：%d 条结果 / %d 个 URL（判定：基线已建立，下次巡检开始比对）", len(cites), len(urls))
	case diff.Changed:
		diff.Summary = fmt.Sprintf("新增 %d 条 / 消失 %d 条，SimHash 距离 %d（判定：有增量变化）", len(diff.NewURLs), len(diff.GoneURLs), diff.HammingDistance)
	case len(diff.NewURLs)+len(diff.GoneURLs) > 0:
		// The page set moved a little but not enough to call it a new
		// story: say both, so a swapped result isn't hidden.
		diff.Summary = fmt.Sprintf("快照稳定，SimHash 距离 %d，新增 %d 条 / 消失 %d 条（判定：无实质变化）", diff.HammingDistance, len(diff.NewURLs), len(diff.GoneURLs))
	default:
		diff.Summary = fmt.Sprintf("快照稳定，SimHash 距离 %d（判定：无实质变化）", diff.HammingDistance)
	}

	job.LastCheckedAt = diff.CheckedAt
	job.DiffDetected = diff.Changed
	if diff.Changed {
		job.StatusBadge = "🚨 发现增量变化 (SUPERSEDES)"
	} else {
		job.StatusBadge = "✅ 快照稳定 (VERIFIED)"
	}
	job.LatestDiff = diff.Summary
	if len(cites) > 0 {
		job.SourceURL = cites[0].URL
	}
	job.SimHash = hash
	job.TopDomains = strings.Join(urls, "|")
	// What a watch keeps is shown again on every page load, so it goes
	// through the same field pass as a result: a phone in a title or a
	// token in a URL is not persisted.
	job = SanitizeWatch(job)
	// The stored query stays the one the input gate produced: it is what
	// the next check must send (a [PHONE] marker would search for a
	// marker). It carries no personal data.
	job.Query = maskedQuery
	diff.Summary = SanitizeText(diff.Summary)
	for i := range diff.NewURLs {
		diff.NewURLs[i] = SanitizeText(diff.NewURLs[i])
	}
	for i := range diff.GoneURLs {
		diff.GoneURLs[i] = SanitizeText(diff.GoneURLs[i])
	}
	for i := range diff.TopURLs {
		diff.TopURLs[i] = SanitizeText(diff.TopURLs[i])
	}
	return diff, job, nil
}

// SanitizeWatch masks the fields a saved watch keeps. It runs before a job
// is stored and again on the way out, so a row written before the gate
// existed is not shown as it was written.
func SanitizeWatch(j StakeoutJob) StakeoutJob {
	j.Query = SanitizeText(j.Query)
	j.Title = SanitizeText(j.Title)
	j.SearchParams = SanitizeText(j.SearchParams)
	j.StatusBadge = SanitizeText(j.StatusBadge)
	j.LatestDiff = SanitizeText(j.LatestDiff)
	j.SourceURL = SanitizeText(j.SourceURL)
	j.TopDomains = SanitizeText(j.TopDomains)
	return j
}

// PrepareWatchQuery is the gate a saved watch passes before it is stored or
// re-checked: a query that hits the red line is refused, and personal data
// is masked so an unattended watch never persists what an investigate call
// would not send. A policy that cannot be read fails closed.
func PrepareWatchQuery(query string) (string, error) {
	audit, _ := redactForInvestigationPolicy(query)
	if audit.ComplianceStatus == "BLOCKED_REDLINE" {
		return "", fmt.Errorf("watch query refused: %s", audit.BlockedReason)
	}
	return audit.MaskedQuery, nil
}

// SnapshotStakeout takes one live snapshot of a query without comparing it
// to anything: it asks every enabled channel, merges and ranks the results,
// and returns the SimHash, the URLs in rank order, those URLs pipe-joined
// (the form a StakeoutJob stores in TopDomains) and the citations.
//
// It exists for the first baseline of a job, and for callers that want the
// current picture without a stored snapshot to diff against.
func SnapshotStakeout(ctx context.Context, query string, k Keys) (hash uint64, urls []string, topDomains string, cites []Citation, err error) {
	query, err = PrepareWatchQuery(query)
	if err != nil {
		return 0, nil, "", nil, err
	}
	hash, urls, cites, err = snapshotRun(ctx, query, k)
	if err != nil {
		return 0, nil, "", nil, err
	}
	for i := range urls {
		urls[i] = SanitizeText(urls[i])
	}
	return hash, urls, strings.Join(urls, "|"), cites, nil
}

// snapshotRun is the one place a snapshot is taken, shared by
// SnapshotStakeout and CheckStakeout so both fold results the same way.
func snapshotRun(ctx context.Context, query string, k Keys) (uint64, []string, []Citation, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return 0, nil, nil, errors.New("stakeout: empty query")
	}
	runs := runChannels(ctx, query, k, 10, nil, 0)
	if len(runs) == 0 {
		return 0, nil, nil, errors.New("stakeout: no channel is enabled")
	}

	var all []Citation
	hits, failed := 0, 0
	var firstErr error
	for _, r := range runs {
		hits += r.run.RawHits
		all = append(all, r.cite...)
		if r.run.Err != nil {
			failed++
			if firstErr == nil {
				firstErr = r.run.Err
			}
		}
	}
	if failed == len(runs) {
		return 0, nil, nil, fmt.Errorf("stakeout: every channel failed (%d/%d, first: %v)", failed, len(runs), firstErr)
	}
	if hits == 0 {
		return 0, nil, nil, errors.New("stakeout: every channel returned no hits")
	}

	merged := mergeCitations(all, stakeoutSnapshotCap)
	if len(merged) == 0 {
		return 0, nil, nil, errors.New("stakeout: no citation carried a usable URL")
	}
	ranked := rankCitations(merged, query, time.Now(), rankConfig{})

	urls := make([]string, 0, len(ranked))
	for _, c := range ranked {
		urls = append(urls, c.URL)
	}

	// Hash over URL-sorted text: channels return in a different order run
	// to run, and the snapshot must not look changed just because they did.
	byURL := make([]Citation, len(ranked))
	copy(byURL, ranked)
	sort.Slice(byURL, func(i, j int) bool { return byURL[i].URL < byURL[j].URL })
	var b strings.Builder
	for _, c := range byURL {
		b.WriteString(c.Title)
		b.WriteByte(' ')
		b.WriteString(c.Snippet)
		b.WriteByte('\n')
	}
	return simhash64(b.String()), urls, ranked, nil
}

// parseSnapshotURLs reads a job's saved URL list back out of TopDomains,
// where URLs are joined by '|'.
func parseSnapshotURLs(topDomains string) []string {
	if strings.TrimSpace(topDomains) == "" {
		return nil
	}
	parts := strings.Split(topDomains, "|")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// stakeoutURLKey is the identity two URLs are diffed by: the normalized
// form, so the same page found by two channels counts once, falling back to
// the trimmed raw URL when it doesn't parse.
func stakeoutURLKey(raw string) string {
	if k := normalizeURL(raw); k != "" {
		return k
	}
	return strings.TrimSpace(raw)
}

// diffURLs reports which URLs the current snapshot added and which ones the
// previous snapshot had and this one doesn't. Both lists keep the URLs as
// the snapshots store them, so the page can link them.
func diffURLs(prev, cur []string) (added, gone []string) {
	prevSet := make(map[string]bool, len(prev))
	for _, u := range prev {
		prevSet[stakeoutURLKey(u)] = true
	}
	curSet := make(map[string]bool, len(cur))
	for _, u := range cur {
		curSet[stakeoutURLKey(u)] = true
	}
	for _, u := range cur {
		if k := stakeoutURLKey(u); !prevSet[k] {
			added = append(added, u)
		}
	}
	for _, u := range prev {
		if k := stakeoutURLKey(u); !curSet[k] {
			gone = append(gone, u)
		}
	}
	return added, gone
}

// NewStakeoutJob builds a job as the "new watch" form creates it: an id (a
// base-36 nanosecond stamp when the caller gives none), the interval taken
// from the cron text when intervalSec is 0, and the waiting badge. No
// snapshot exists yet, so SimHash and TopDomains stay empty and the first
// check becomes the baseline.
func NewStakeoutJob(id, title, query, cron string, intervalSec int) StakeoutJob {
	cron = strings.TrimSpace(cron)
	if cron == "" {
		cron = "每 1 小时"
	}
	if strings.TrimSpace(id) == "" {
		id = "stk_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	if strings.TrimSpace(title) == "" {
		title = firstRunesOf(strings.TrimSpace(query), 40)
	}
	interval := intervalSec
	if interval <= 0 {
		interval = ParseCronInterval(cron)
	}
	return StakeoutJob{
		ID:          id,
		Title:       title,
		Query:       query,
		Cron:        cron,
		Interval:    interval,
		StatusBadge: "⏳ 待首次巡检",
	}
}

var (
	reCronEveryMin  = regexp.MustCompile(`每\s*(\d+)\s*分钟`)
	reCronEveryHour = regexp.MustCompile(`每\s*(\d+)\s*个?\s*小时`)
	reCronEverySec  = regexp.MustCompile(`每\s*(\d+)\s*秒`)
	reCronEveryWeek = regexp.MustCompile(`每\s*(\d+)\s*个?\s*(?:周|星期)`)
	reCronEveryDay  = regexp.MustCompile(`每\s*(\d+)\s*[天日]`)
	reCronDaily     = regexp.MustCompile(`每日|每天|daily`)
	reCronHourly    = regexp.MustCompile(`每小时|hourly`)
	reCronWeekly    = regexp.MustCompile(`每周|每星期|weekly`)
)

// ParseCronInterval turns the cron text the job form offers into a check
// interval in seconds: "每 30 分钟" is 1800, "每 1 小时" is 3600, "每日
// 09:00" is 86400, "每 2 天" is 172800. Text it cannot read falls back to
// one hour, the default the form starts at, rather than to zero (which
// would mean "never").
func ParseCronInterval(cron string) int {
	cron = strings.TrimSpace(cron)
	if cron == "" {
		return 3600
	}
	if m := reCronEveryMin.FindStringSubmatch(cron); m != nil {
		if n := atoiOr(m[1], 0); n > 0 {
			return n * 60
		}
	}
	if m := reCronEveryHour.FindStringSubmatch(cron); m != nil {
		if n := atoiOr(m[1], 0); n > 0 {
			return n * 3600
		}
	}
	if m := reCronEverySec.FindStringSubmatch(cron); m != nil {
		if n := atoiOr(m[1], 0); n > 0 {
			return n
		}
	}
	if m := reCronEveryWeek.FindStringSubmatch(cron); m != nil {
		if n := atoiOr(m[1], 0); n > 0 {
			return n * 7 * 86400
		}
	}
	if m := reCronEveryDay.FindStringSubmatch(cron); m != nil {
		if n := atoiOr(m[1], 0); n > 0 {
			return n * 86400
		}
	}
	switch {
	case reCronDaily.MatchString(cron):
		return 86400
	case reCronWeekly.MatchString(cron):
		return 7 * 86400
	case reCronHourly.MatchString(cron):
		return 3600
	}
	return 3600
}

// NextCheckAt is when a job is next due. A job never checked is due at
// once, so the first tick takes its baseline. A stamp that won't parse
// means "unknown", not "overdue": the job waits one interval from now
// instead of being re-checked every tick.
func NextCheckAt(job StakeoutJob, now time.Time) time.Time {
	interval := job.Interval
	if interval <= 0 {
		interval = ParseCronInterval(job.Cron)
	}
	if interval <= 0 {
		interval = 3600
	}
	every := time.Duration(interval) * time.Second

	if strings.TrimSpace(job.LastCheckedAt) == "" {
		return now
	}
	last, ok := parseDate(job.LastCheckedAt)
	if !ok {
		return now.Add(every)
	}
	return last.Add(every)
}

// StakeoutScheduler runs the saved jobs in the background. It owns no
// storage: the list callback says what to watch and the save callback
// persists what a check produced, so the caller's own store stays the
// single writer.
type StakeoutScheduler struct {
	k    Keys
	list func() []StakeoutJob
	save func(StakeoutJob) error

	mu       sync.Mutex
	running  map[string]bool
	cancel   context.CancelFunc
	loopDone chan struct{}
	wg       sync.WaitGroup
}

// NewStakeoutScheduler builds a scheduler over the caller's job store.
// list is read on every tick and must return a copy; save is called once
// per finished check and may be nil when the caller persists elsewhere.
func NewStakeoutScheduler(k Keys, list func() []StakeoutJob, save func(StakeoutJob) error) *StakeoutScheduler {
	return &StakeoutScheduler{
		k:       k,
		list:    list,
		save:    save,
		running: map[string]bool{},
	}
}

// Start begins watching for due jobs on ctx. It returns at once; checks run
// in their own goroutines. Calling Start on an already running scheduler is
// a no-op, so a stray second call cannot leave two loops behind.
func (s *StakeoutScheduler) Start(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	s.cancel = cancel
	s.loopDone = done
	go s.loop(runCtx, done)
}

// Stop cancels the loop, waits for it to leave, then waits for every check
// it started. The order matters: the loop is the only thing that adds to
// the wait group, so waiting for it first means no Add can race the Wait.
// After Stop returns no goroutine this scheduler started is still running.
func (s *StakeoutScheduler) Stop() {
	s.mu.Lock()
	cancel, done := s.cancel, s.loopDone
	s.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	if done != nil {
		<-done
	}
	s.wg.Wait()
	// The running marker stays set until the wait group has drained, so a
	// Start racing with Stop sees a live scheduler and does nothing
	// instead of adding a goroutine the Wait above would miss.
	s.mu.Lock()
	s.cancel, s.loopDone = nil, nil
	s.mu.Unlock()
}

// loop is the ticker: every stakeoutTick it hands the due jobs to their own
// goroutines and goes back to waiting.
func (s *StakeoutScheduler) loop(ctx context.Context, done chan struct{}) {
	defer close(done)
	t := time.NewTicker(stakeoutTick)
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

// runDue starts a check for every job whose NextCheckAt has passed. A job
// already being checked is skipped, so a slow channel run cannot stack a
// second check of the same query on top of the first.
func (s *StakeoutScheduler) runDue(ctx context.Context, now time.Time) {
	if s.list == nil {
		return
	}
	for _, job := range s.list() {
		if ctx.Err() != nil {
			return
		}
		if NextCheckAt(job, now).After(now) {
			continue
		}
		if !s.begin(job.ID) {
			continue
		}
		job := job
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer s.end(job.ID)
			diff, updated, err := CheckStakeout(ctx, job, s.k)
			if err != nil {
				// A cancelled check is shutdown, not a failure to report.
				if ctx.Err() == nil {
					log.Printf("stakeout: %s: %v", job.ID, err)
				}
				return
			}
			log.Printf("stakeout: %s: %s", job.ID, diff.Summary)
			if s.save == nil {
				return
			}
			if err := s.save(updated); err != nil {
				log.Printf("stakeout: %v", err)
			}
		}()
	}
}

// CheckNow checks one job immediately, whatever its schedule says, and
// saves the updated snapshot. The job must exist in the list callback's
// current answer. A job already being checked returns an error rather than
// running twice at once. A failed save is logged and the diff still
// returned: the check itself is real even when the store refused it.
func (s *StakeoutScheduler) CheckNow(ctx context.Context, jobID string) (StakeoutDiff, error) {
	var job StakeoutJob
	found := false
	if s.list != nil {
		for _, j := range s.list() {
			if j.ID == jobID {
				job = j
				found = true
				break
			}
		}
	}
	if !found {
		return StakeoutDiff{}, fmt.Errorf("stakeout: job %q not found", jobID)
	}
	if !s.begin(jobID) {
		return StakeoutDiff{}, fmt.Errorf("stakeout: job %q is already being checked", jobID)
	}
	defer s.end(jobID)

	diff, updated, err := CheckStakeout(ctx, job, s.k)
	if err != nil {
		return diff, err
	}
	if s.save != nil {
		if err := s.save(updated); err != nil {
			log.Printf("stakeout: %v", err)
		}
	}
	return diff, nil
}

// begin claims a job for one check, false when another check holds it.
func (s *StakeoutScheduler) begin(jobID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running == nil {
		s.running = map[string]bool{}
	}
	if s.running[jobID] {
		return false
	}
	s.running[jobID] = true
	return true
}

// end releases a job's claim.
func (s *StakeoutScheduler) end(jobID string) {
	s.mu.Lock()
	delete(s.running, jobID)
	s.mu.Unlock()
}
