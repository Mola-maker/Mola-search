// The investigation engine: one query in, a verdict out. The plan comes from
// the mode, the evidence from live channels, the verdict from a support
// score — and when support is missing it says so (ABSTAIN) instead of
// inventing an answer.
package searchdet

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Options is what one investigate call was asked to do.
type Options struct {
	Query    string
	ModeID   string
	DemoID   string
	Debate   bool
	EnableCU bool
	// Origin names who asked: the Detective page, or an MCP client's own
	// name. It is carried into every trajectory step so a run found in the
	// proof tab can be traced back to the agent that requested it. Empty
	// means the page's UI.
	Origin string
	// Image, when set, is an uploaded screenshot the M22/M26 path analyses.
	ImageName string
	ImageData []byte
	// PageURL is what the DOM snapshot path opens; empty means the first
	// citation.
	PageURL string
	// Channels, when set, is the only providers this run may ask: an
	// agent's own route stack. Empty means every provider the user has on.
	Channels []string
}

// Engine runs investigations against live channels.
type Engine struct {
	// keysFn lets a caller hand in keys (tests) instead of the env lookup.
	keysFn func() Keys
	// nowFn holds the clock, so decay and ages are testable.
	nowFn func() time.Time
	// channelRunner is swapped in tests to keep them off the network.
	channelRunner func(ctx context.Context, query string, k Keys, n int, only map[string]bool) []runResult
	// settings are the gates and thresholds the page can change.
	settingsMu  sync.RWMutex
	settings    Settings
	settingsErr error
	// health is each provider's circuit-breaker state and measured use.
	healthMu sync.Mutex
	health   map[string]*ChannelHealth
}

// ChannelHealth is one provider's measured state: how often it ran, what it
// returned, and whether its breaker is open after consecutive errors. It is
// the honest replacement for a vendor quota the process cannot read.
type ChannelHealth struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Keyed            bool   `json:"keyed"`
	Enabled          bool   `json:"enabled"`
	BreakerOpen      bool   `json:"breakerOpen"`
	BreakerOpenUntil string `json:"breakerOpenUntil,omitempty"`
	ConsecutiveFails int    `json:"consecutiveFails"`
	Calls            int    `json:"calls"`
	Hits             int    `json:"hits"`
	LastError        string `json:"lastError,omitempty"`
}

// NewEngine is the engine with live channels and env keys.
func NewEngine() *Engine {
	e := &Engine{
		keysFn:   ResolveKeys,
		nowFn:    time.Now,
		health:   map[string]*ChannelHealth{},
		settings: DefaultSettings(),
	}
	e.settings, e.settingsErr = LoadSettings()
	e.channelRunner = e.runLiveChannels
	return e
}

// Settings returns the current gates. The copy is safe to read.
func (e *Engine) Settings() Settings {
	e.settingsMu.RLock()
	defer e.settingsMu.RUnlock()
	return e.settings
}

// SettingsError says why the settings file could not be used, nil when it
// loaded.
func (e *Engine) SettingsError() error {
	e.settingsMu.RLock()
	defer e.settingsMu.RUnlock()
	return e.settingsErr
}

// SetSettings replaces the gates. The caller persists them if it wants them
// to survive a restart; the engine only applies them.
func (e *Engine) SetSettings(s Settings) error {
	if err := s.Validate(); err != nil {
		return err
	}
	e.settingsMu.Lock()
	e.settings = s
	e.settingsErr = nil
	e.settingsMu.Unlock()
	return nil
}

// SetChannelEnabled turns one provider on or off. Every provider is equal:
// turning one off leaves the rest serving the run.
func (e *Engine) SetChannelEnabled(id string, on bool) error {
	if channelByID(id) == nil {
		return fmt.Errorf("unknown channel %q", id)
	}
	e.settingsMu.Lock()
	defer e.settingsMu.Unlock()
	next := make([]string, 0, len(e.settings.DisabledChannels))
	for _, d := range e.settings.DisabledChannels {
		if d != id {
			next = append(next, d)
		}
	}
	if !on {
		next = append(next, id)
	}
	e.settings.DisabledChannels = next
	return nil
}

// channelOnly is a run's provider allow-list, nil for every allowed one.
func channelOnly(opt Options) map[string]bool {
	if len(opt.Channels) == 0 {
		return nil
	}
	only := make(map[string]bool, len(opt.Channels))
	for _, id := range opt.Channels {
		only[id] = true
	}
	return only
}

// ChannelInfo is one source's identity and whether its own keys are
// present. A route registry says a source is ready from this, not from
// whether the user left it on.
type ChannelInfo struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Keyed      bool   `json:"keyed"`
	Configured bool   `json:"configured"`
}

// ChannelInfos are every source the engine knows, in the engine's order.
func ChannelInfos(k Keys) []ChannelInfo {
	cs := channels()
	out := make([]ChannelInfo, 0, len(cs))
	for _, c := range cs {
		out = append(out, ChannelInfo{ID: c.ID(), Name: c.Name(), Keyed: keyedChannel(c.ID()), Configured: c.Enabled(k)})
	}
	return out
}

// keyedChannel says which providers need a key; the page groups them.
func (e *Engine) ChannelStatuses() []ChannelHealth {
	st := e.Settings()
	e.healthMu.Lock()
	defer e.healthMu.Unlock()
	out := make([]ChannelHealth, 0, len(channels()))
	for _, c := range channels() {
		h := ChannelHealth{ID: c.ID(), Name: c.Name(), Keyed: keyedChannel(c.ID()), Enabled: !st.Disabled(c.ID())}
		if cur := e.health[c.ID()]; cur != nil {
			h.BreakerOpen = cur.BreakerOpen
			h.BreakerOpenUntil = cur.BreakerOpenUntil
			h.ConsecutiveFails = cur.ConsecutiveFails
			h.Calls = cur.Calls
			h.Hits = cur.Hits
			h.LastError = cur.LastError
		}
		out = append(out, h)
	}
	return out
}

// allowedChannels is which providers a run may ask: the caller's allowlist,
// the user's per-provider switches, and each provider's breaker. A provider
// missing from the result is not asked at all.
func (e *Engine) allowedChannels(only map[string]bool) map[string]bool {
	st := e.Settings()
	allow := map[string]bool{}
	for _, c := range channels() {
		id := c.ID()
		if only != nil && !only[id] {
			continue
		}
		if st.Disabled(id) || e.breakerOpen(id) {
			continue
		}
		allow[id] = true
	}
	return allow
}

// runLiveChannels is the default runner: it applies the user's provider
// switches and each open breaker, runs what is left, and records the truth
// about each one.
func (e *Engine) runLiveChannels(ctx context.Context, query string, k Keys, n int, only map[string]bool) []runResult {
	allow := e.allowedChannels(only)
	if len(allow) == 0 {
		return nil
	}
	res := runChannels(ctx, query, k, n, allow, e.Settings().ChannelTimeout())
	e.recordHealth(res, e.Settings())
	return res
}

func (e *Engine) breakerOpen(id string) bool {
	e.healthMu.Lock()
	defer e.healthMu.Unlock()
	h := e.health[id]
	if h == nil || !h.BreakerOpen {
		return false
	}
	until, err := time.Parse(time.RFC3339, h.BreakerOpenUntil)
	if err == nil && time.Now().After(until) {
		h.BreakerOpen = false
		h.BreakerOpenUntil = ""
		h.ConsecutiveFails = 0
		return false
	}
	return true
}

func (e *Engine) recordHealth(res []runResult, st Settings) {
	e.healthMu.Lock()
	defer e.healthMu.Unlock()
	open := func(id string, h *ChannelHealth) {
		h.BreakerOpen = true
		h.BreakerOpenUntil = time.Now().Add(time.Duration(st.BreakerCooldownSec) * time.Second).Format(time.RFC3339)
	}
	for _, rr := range res {
		id := rr.run.ChannelID
		h := e.health[id]
		if h == nil {
			h = &ChannelHealth{ID: id, Name: rr.run.Name}
			e.health[id] = h
		}
		h.Calls++
		h.Hits += rr.run.RawHits
		if rr.run.Err != nil {
			h.ConsecutiveFails++
			h.LastError = rr.run.Err.Error()
			if h.ConsecutiveFails >= st.BreakerFails {
				open(id, h)
			}
			continue
		}
		h.ConsecutiveFails = 0
		h.LastError = ""
	}
}

// keyedChannel says which providers need a key; the page groups them.
func keyedChannel(id string) bool {
	switch id {
	case "tencent", "tavily", "exa", "brave", "searxng":
		return true
	}
	return false
}

// Investigate runs one query through the mode's plan.
func (e *Engine) Investigate(ctx context.Context, opt Options) Investigation {
	started := e.nowFn()
	startedWall := time.Now()
	keys := e.keysFn()
	origin := strings.TrimSpace(opt.Origin)
	if origin == "" {
		origin = "Detective UI"
	}
	inv := Investigation{
		TraceID:         fmt.Sprintf("trc_%d", started.UnixNano()),
		Query:           opt.Query,
		ModeID:          opt.ModeID,
		OriginAgent:     origin,
		Citations:       []Citation{},
		ClaimMatrix:     []ClaimMatrixRow{},
		TemporalTree:    []TemporalTreeNode{},
		MultimodalCards: []MultimodalCard{},
		Trajectory:      []TrajectoryStep{},
		ChannelHits:     map[string]int{},
		GeneratedAt:     started.Format(time.RFC3339),
	}
	if inv.ModeID == "" {
		inv.ModeID = "M01"
	}
	if mode, ok := ModeOf(inv.ModeID); ok {
		inv.ModeName = mode.NameZh
	}
	if opt.Query == "" {
		inv.Verdict = "ABSTAIN"
		inv.AbstainReason = "查询为空，没有可核验的内容。"
		inv.ExecutiveSummary = "🛑 请先输入一个待核验的问题。"
		inv.PrimaryEngine = "MolaSearch 输入校验"
		return inv
	}

	// 1) Privacy and compliance gate. The terms are kept for the output side:
	//    a result that carries one back is dropped before any view is built.
	redactAudit, redlineTerms := redactForInvestigationPolicy(opt.Query)
	inv.RedactAudit = redactAudit
	redactDur := time.Since(startedWall)
	if inv.RedactAudit.ComplianceStatus == "BLOCKED_REDLINE" {
		inv.Verdict = "ABSTAIN"
		inv.TrustScore = 100
		inv.PrimaryEngine = "Magpie Redact × 合规红线防火墙"
		inv.ActiveModeBadge = inv.ModeID + " 合规拦截"
		inv.ExecutiveSummary = "🛑 已拦截：" + inv.RedactAudit.BlockedReason + " 未发起任何外部检索。"
		inv.AbstainReason = inv.RedactAudit.BlockedReason
		inv.Debate = Debate{
			Pro:          []string{"合规守门员检测请求意图"},
			Con:          []string{"命中红线规则，立即阻断外发"},
			JudgeSummary: "严格执行合规红线：不碰时政和涉及个人的消息。",
		}
		inv.TotalLatencyMs = int(e.nowFn().Sub(started).Milliseconds())
		return inv
	}

	// 2) The mode's real plan. A provider the user turned off is skipped by
	//    the runner; the remaining providers still serve the run, and the
	//    verdict says what the evidence supports (ABSTAIN only if nothing
	//    came back).
	safeQuery := inv.RedactAudit.MaskedQuery
	planStart := time.Now()
	plan := planFor(inv.ModeID, safeQuery)
	planDur := time.Since(planStart)
	inv.NoPersist = plan.NoPersist
	inv.ModeNote = plan.Note
	inv.ActiveModeBadge = inv.ModeID + " " + inv.ModeName
	cfg := e.Settings()

	// 4) Ask every plan query through the channels, in parallel, and record
	//    each real run in the trajectory.
	queries := plan.Queries
	if plan.HyDE {
		if draft, err := hydeDraft(ctx, keys, safeQuery); err == nil {
			queries = append([]subQuery{{Role: "HyDE草稿", Q: draft}}, queries...)
			inv.ModeNote += "（草稿已生成）"
		} else {
			inv.ModeNote += "（" + err.Error() + "）"
		}
	}
	if plan.ProCon {
		queries = append(queries, antiQueries(safeQuery)...)
	}
	type qr struct {
		sub  subQuery
		runs []runResult
		took time.Duration
	}
	only := channelOnly(opt)
	results := make([]qr, len(queries))
	var wg sync.WaitGroup
	qStart := time.Now()
	for i, sq := range queries {
		i, sq := i, sq
		wg.Add(1)
		go func() {
			defer wg.Done()
			t0 := time.Now()
			results[i] = qr{sub: sq, runs: e.channelRunner(ctx, sq.Q, keys, cfg.PerChannel, only), took: time.Since(t0)}
		}()
	}
	wg.Wait()
	qTotal := time.Since(qStart)

	var all []Citation
	var runs []channelRun
	for _, r := range results {
		for _, rr := range r.runs {
			all = append(all, rr.cite...)
			runs = append(runs, rr.run)
			startMs := 0
			if !rr.run.At.IsZero() {
				startMs = int(rr.run.At.Sub(startedWall).Milliseconds())
			}
			if startMs < 0 {
				startMs = 0
			}
			endMs := startMs + int(rr.run.Latency.Milliseconds())
			step := TrajectoryStep{
				OriginAgent:     origin,
				ModeID:          inv.ModeID,
				Gateway:         rr.run.Name,
				ChannelID:       rr.run.ChannelID,
				Params:          r.sub.Role + ": " + truncate(r.sub.Q, 60),
				StartOffsetMs:   startMs,
				EndOffsetMs:     endMs,
				DurationMs:      int(rr.run.Latency.Milliseconds()),
				RawHits:         rr.run.RawHits,
				TimingBreakdown: fmt.Sprintf("%s %s", r.sub.Role, fmtMs(rr.run.Latency)),
			}
			if rr.run.Err != nil {
				step.ReturnSummary = "❌ " + rr.run.Err.Error()
			} else {
				step.ReturnSummary = fmt.Sprintf("✅ 真实抓回 %d 条结果", rr.run.RawHits)
				if len(rr.cite) > 0 {
					step.TopURL = rr.cite[0].URL
					step.TopDomain = domainOf(rr.cite[0].URL)
				}
			}
			inv.Trajectory = append(inv.Trajectory, step)
		}
	}
	// The engine named on the report is the one that actually answered, not
	// a vendor picked in advance.
	inv.PrimaryEngine = primaryEngineOf(runs)

	// 4a) AREX gap rounds: ask what the first round left unanswered.
	if plan.GapRounds > 0 {
		gapStart := time.Now()
		safeOpt := opt
		safeOpt.Query = safeQuery
		extra, steps := e.gapRounds(ctx, &inv, safeOpt, keys, plan, cfg, all, startedWall, origin, only)
		all = append(all, extra...)
		inv.Trajectory = append(inv.Trajectory, steps...)
		qTotal += time.Since(gapStart)
	}

	// 4b) Citation snowball: the domains that answered the first round are
	//     asked for their own coverage of the subject, so a secondary report
	//     leads to the primary source it quoted.
	if plan.Snowball {
		extra, steps := e.snowball(ctx, keys, safeQuery, all, startedWall, origin, only)
		all = append(all, extra...)
		inv.Trajectory = append(inv.Trajectory, steps...)
	}

	rankStart := time.Now()
	merged := mergeCitations(all, cfg.MergeCap)
	inv.ChannelHits = channelHitCounts(merged)
	now := e.nowFn()
	ranked := rankCitations(merged, opt.Query, now, rankConfig{
		AuthBoost: plan.AuthBoost, FreshOnly: plan.FreshOnly,
	})
	folded, foldedCount := foldResults(ranked, now, cfg.FoldGamma)
	if plan.NoFold {
		folded, foldedCount = ranked, 0
	}
	rankDur := time.Since(rankStart)
	inv.CollapsedSpamCount = foldedCount
	inv.Citations = folded
	if len(inv.Citations) > cfg.TopK {
		inv.Citations = inv.Citations[:cfg.TopK]
	}
	if plan.AuthBoost {
		inv.Citations = authFilter(inv.Citations, cfg.AuthorityLow)
	}
	if plan.FreshOnly {
		inv.Citations = freshFilter(inv.Citations, now, cfg.FreshDays)
	}

	// 5) Second hop: ask again about the entities the first round surfaced.
	if plan.Hops > 0 {
		inv.Trajectory = append(inv.Trajectory, e.hop(ctx, &inv, opt, keys, plan, cfg, redlineTerms, only)...)
	}

	// 5a) Output-side gate: a result that carries a red-line term back is
	// dropped, and personal data in every kept field is masked, before the
	// verdict, the multimodal checks and every derived view read the evidence.
	outputAudit := guardEvidence(&inv, redlineTerms)

	// 6) Modality checks the mode asked for.
	inv.MultimodalCards = e.multimodal(ctx, opt, keys, plan, inv.Citations)

	// 7) Verdict from real support.
	verdict, trust, reason := assess(opt.Query, inv.Citations, plan, cfg)
	inv.Verdict, inv.TrustScore = verdict, trust
	inv.AbstainReason = reason
	inv.ClaimMatrix = claimRows(opt.Query, inv.Citations, verdict)
	if plan.Temporal {
		inv.TemporalTree = temporalTree(inv.Citations, opt.Query)
	}
	inv.Debate = debateOf(opt.Query, inv.Citations, verdict, plan.ProCon)
	inv.FusionNetwork = fusionOf(opt, inv, runs, plan.Graph)
	// 8) The answer: a real model reads the guarded citations and states what
	//    they support. Skipped for ABSTAIN and when the user turned it off; a
	//    failed call leaves the evidence envelope exactly as it was.
	if cfg.AnswerEnabled && verdict != "ABSTAIN" && len(inv.Citations) > 0 {
		answerAt := int(time.Since(startedWall).Milliseconds())
		ans := synthesizeAnswer(ctx, keys, opt.Query, inv.Citations, cfg.AnswerModel)
		if ans.Text != "" {
			ans.Text = guardString(ans.Text, outputAudit)
			if redlineHit(ans.Text, redlineTerms) != "" {
				ans.Status, ans.Error, ans.Text = "error", "答案命中红线，已丢弃", ""
			}
		}
		inv.Answer = ans
		summary := "未合成答案"
		if ans.Status == "ok" {
			summary = "✅ 已用 " + itoa(len(ans.CiteIndexes)) + " 条引用合成答案"
		} else if ans.Error != "" {
			summary = "❌ " + ans.Error
		}
		inv.Trajectory = append(inv.Trajectory, TrajectoryStep{
			OriginAgent:   origin,
			ModeID:        inv.ModeID,
			Gateway:       "答案合成 · " + ans.Model,
			Params:        "问题 + 前 " + itoa(len(inv.Citations)) + " 条证据",
			StartOffsetMs: answerAt,
			EndOffsetMs:   answerAt + ans.LatencyMs,
			DurationMs:    ans.LatencyMs,
			RawHits:       len(ans.CiteIndexes),
			ReturnSummary: summary,
		})
	} else {
		inv.Answer = Answer{Status: "disabled"}
	}
	inv.ExecutiveSummary = summaryOf(opt.Query, inv, verdict, reason)
	applyStageTiming(inv.Trajectory, time.Since(startedWall), redactDur, planDur, qTotal, rankDur)
	// 9) Output-side gate, second half: cards and everything derived from the
	//    evidence are masked too, and the audit is what the page and MCP see.
	guardOutput(&inv, redlineTerms, outputAudit)
	inv.TotalLatencyMs = int(e.nowFn().Sub(started).Milliseconds())
	// The briefing and the envelope quote the run's latency, so they are
	// built once it is known — and then masked, because the briefing's title
	// carries the user's own query, which may hold an ID card or a phone.
	inv.MarkdownBriefing = guardString(briefingOf(inv), outputAudit)
	inv.AgentEnvelopeXML = guardString(envelopeOf(inv), outputAudit)
	inv.OutputGuard = *outputAudit
	return inv
}

// snowball asks the domains that answered the first round for their own
// coverage of the same subject. A secondary report usually names the primary
// source; searching the source's own site finds it. At most three domains
// are followed, so one round cannot fan out without bound.
func (e *Engine) snowball(ctx context.Context, keys Keys, query string, cites []Citation, started time.Time, origin string, only map[string]bool) ([]Citation, []TrajectoryStep) {
	merged := mergeCitations(cites, e.Settings().MergeCap)
	var domains []string
	seen := map[string]bool{}
	for _, c := range merged {
		d := domainOf(c.URL)
		if d == "" || seen[d] {
			continue
		}
		seen[d] = true
		domains = append(domains, d)
		if len(domains) == 3 {
			break
		}
	}
	subject := coreSubject(query)
	var out []Citation
	var steps []TrajectoryStep
	for _, d := range domains {
		q := "site:" + d + " " + subject
		for _, rr := range e.channelRunner(ctx, q, keys, e.Settings().PerChannel, only) {
			out = append(out, rr.cite...)
			startMs := 0
			if !rr.run.At.IsZero() {
				startMs = int(rr.run.At.Sub(started).Milliseconds())
			}
			if startMs < 0 {
				startMs = 0
			}
			step := TrajectoryStep{
				OriginAgent:     origin,
				ModeID:          "M08",
				Gateway:         rr.run.Name,
				ChannelID:       rr.run.ChannelID,
				Params:          "引文雪球 site:" + d + ": " + truncate(q, 60),
				StartOffsetMs:   startMs,
				EndOffsetMs:     startMs + int(rr.run.Latency.Milliseconds()),
				DurationMs:      int(rr.run.Latency.Milliseconds()),
				RawHits:         rr.run.RawHits,
				TimingBreakdown: "引文雪球 " + fmtMs(rr.run.Latency),
			}
			if rr.run.Err != nil {
				step.ReturnSummary = "❌ " + rr.run.Err.Error()
			} else {
				step.ReturnSummary = fmt.Sprintf("✅ 雪球追回 %d 条一手候选", rr.run.RawHits)
				if len(rr.cite) > 0 {
					step.TopURL = rr.cite[0].URL
					step.TopDomain = domainOf(rr.cite[0].URL)
				}
			}
			steps = append(steps, step)
		}
	}
	return out, steps
}

// applyStageTiming sets each trajectory step's waterfall to the run's real
// stage shares: how much of the investigation's wall time went to redaction,
// planning, the network and ranking. A stage that took no time is zero, so
// the bar never pads itself with invented widths.
func applyStageTiming(steps []TrajectoryStep, total, redact, plan, network, rank time.Duration) {
	if total <= 0 || len(steps) == 0 {
		return
	}
	pct := func(d time.Duration) int {
		if d <= 0 {
			return 0
		}
		return int(d * 100 / total)
	}
	r, p, n, k := pct(redact), pct(plan), pct(network), pct(rank)
	for i := range steps {
		steps[i].PctRedact = r
		steps[i].PctPlan = p
		steps[i].PctLocal = 0
		steps[i].PctNet = n
		steps[i].PctRerank = k
	}
}

// gapTerms are the query's tokens no citation speaks to — the part of the
// question the first round left unanswered. Latin tokens shorter than two
// characters and duplicates are skipped; at most six are handed to a round.
func gapTerms(query string, cites []Citation) []string {
	spoken := map[string]bool{}
	for _, c := range cites {
		for _, t := range tokenize(c.Title + " " + c.Snippet) {
			spoken[t] = true
		}
	}
	var gaps []string
	seen := map[string]bool{}
	for _, t := range tokenize(query) {
		if spoken[t] || seen[t] || len([]rune(t)) < 2 {
			continue
		}
		seen[t] = true
		gaps = append(gaps, t)
		if len(gaps) == 6 {
			break
		}
	}
	return gaps
}

// gapRounds is the AREX loop: each round asks the question's still-missing
// terms and records what came back. It stops when the gaps close, a round
// adds no URL the run did not already have, or the mode's rounds are spent.
// The settings cap is the user's ceiling over every mode.
func (e *Engine) gapRounds(ctx context.Context, inv *Investigation, opt Options, keys Keys, plan modePlan, cfg Settings, all []Citation, started time.Time, origin string, only map[string]bool) ([]Citation, []TrajectoryStep) {
	rounds := plan.GapRounds
	if rounds > cfg.GapRoundsCap {
		rounds = cfg.GapRoundsCap
	}
	if rounds <= 0 {
		return nil, nil
	}
	known := map[string]bool{}
	for _, c := range all {
		if k := normalizeURL(c.URL); k != "" {
			known[k] = true
		}
	}
	var out []Citation
	var steps []TrajectoryStep
	for r := 0; r < rounds; r++ {
		gaps := gapTerms(opt.Query, append(append([]Citation{}, all...), out...))
		if len(gaps) == 0 {
			break
		}
		q := strings.Join(gaps, " ")
		added := 0
		for _, rr := range e.channelRunner(ctx, q, keys, cfg.PerChannel, only) {
			out = append(out, rr.cite...)
			for _, c := range rr.cite {
				if k := normalizeURL(c.URL); k != "" && !known[k] {
					known[k] = true
					added++
				}
			}
			startMs := 0
			if !rr.run.At.IsZero() {
				startMs = int(rr.run.At.Sub(started).Milliseconds())
			}
			if startMs < 0 {
				startMs = 0
			}
			step := TrajectoryStep{
				OriginAgent:     origin,
				ModeID:          inv.ModeID,
				Gateway:         rr.run.Name,
				ChannelID:       rr.run.ChannelID,
				Params:          "缺口补搜: " + truncate(q, 60),
				StartOffsetMs:   startMs,
				EndOffsetMs:     startMs + int(rr.run.Latency.Milliseconds()),
				DurationMs:      int(rr.run.Latency.Milliseconds()),
				RawHits:         rr.run.RawHits,
				TimingBreakdown: "缺口补搜 " + fmtMs(rr.run.Latency),
			}
			if rr.run.Err != nil {
				step.ReturnSummary = "❌ " + rr.run.Err.Error()
			} else {
				step.ReturnSummary = fmt.Sprintf("✅ 补搜「%s」抓回 %d 条", truncate(q, 30), rr.run.RawHits)
				if len(rr.cite) > 0 {
					step.TopURL = rr.cite[0].URL
					step.TopDomain = domainOf(rr.cite[0].URL)
				}
			}
			steps = append(steps, step)
		}
		if added == 0 {
			break
		}
	}
	return out, steps
}

// primaryEngineOf names the providers that actually answered: the vendors
// that returned at least one hit, in run order. No provider is named in
// advance, so the report cannot claim one carried a run another one did.
func primaryEngineOf(runs []channelRun) string {
	var names []string
	seen := map[string]bool{}
	ran := map[string]bool{}
	for _, r := range runs {
		if r.ChannelID == "" {
			continue
		}
		ran[r.ChannelID] = true
		if seen[r.ChannelID] || r.Err != nil || r.RawHits == 0 {
			continue
		}
		seen[r.ChannelID] = true
		names = append(names, r.Name)
	}
	if len(names) == 0 {
		return fmt.Sprintf("MolaSearch（%d 个通道运行，均无命中）", len(ran))
	}
	return strings.Join(names, " × ")
}

// channelHitCounts is how many merged citations each provider contributed.
func channelHitCounts(cites []Citation) map[string]int {
	out := map[string]int{}
	for _, c := range cites {
		if c.Channel != "" {
			out[c.Channel]++
		}
	}
	return out
}

// hop walks the entities the first round surfaced: one more search each,
// merged in, so a multi-hop mode really does more than one round. A mode
// with NoveltyStop stops as soon as a round adds no URL the run did not
// already have — the marginal-gain rule, measured, not asserted.
func (e *Engine) hop(ctx context.Context, inv *Investigation, opt Options, keys Keys, plan modePlan, cfg Settings, terms []string, only map[string]bool) []TrajectoryStep {
	var steps []TrajectoryStep
	known := map[string]bool{}
	for _, c := range inv.Citations {
		if k := normalizeURL(c.URL); k != "" {
			known[k] = true
		}
	}
	ents := entitiesIn(joinCites(inv.Citations), 4)
	rounds := plan.Hops
	if plan.NoveltyStop && rounds > cfg.NoveltyRoundsCap {
		rounds = cfg.NoveltyRoundsCap
	}
	for h := 0; h < rounds; h++ {
		if len(ents) == 0 {
			break
		}
		before := len(known)
		var wg sync.WaitGroup
		out := make([][]runResult, len(ents))
		safe := make([]string, len(ents))
		for i, ent := range ents {
			// An entity lifted out of a sentence can carry a phone number or
			// a key; it is not sent as scraped. A red-line entity is skipped,
			// not searched.
			safe[i] = safeSearchTerm(ent, terms)
			if safe[i] == "" {
				continue
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				out[i] = e.channelRunner(ctx, safe[i], keys, cfg.PerChannel, only)
			}()
		}
		wg.Wait()
		for i, runs := range out {
			for _, rr := range runs {
				for _, c := range rr.cite {
					if k := normalizeURL(c.URL); k != "" {
						known[k] = true
					}
				}
				steps = append(steps, TrajectoryStep{
					OriginAgent: inv.OriginAgent, ModeID: inv.ModeID,
					Gateway: rr.run.Name, ChannelID: rr.run.ChannelID, Params: "实体二跳: " + safe[i],
					DurationMs:      int(rr.run.Latency.Milliseconds()),
					RawHits:         rr.run.RawHits,
					TimingBreakdown: "实体二跳 " + fmtMs(rr.run.Latency),
					ReturnSummary:   fmt.Sprintf("实体「%s」抓回 %d 条", safe[i], rr.run.RawHits),
				})
				inv.Citations = mergeCitations(append(inv.Citations, rr.cite...), cfg.TopK)
			}
		}
		if plan.NoveltyStop && len(known) == before {
			break
		}
		ents = entitiesIn(joinCites(inv.Citations), 4)
	}
	return steps
}

// assess decides the verdict from what was really found: coverage of the
// query's substance, authority of the sources, and whether anything
// contradicts. A refute or confirm signal is weighted by the authority of
// the source that carries it, and a verdict needs an authoritative source:
// a content farm repeating "官方辟谣" is not an official refutation. Zero
// support is ABSTAIN, never a guess.
func assess(query string, cites []Citation, plan modePlan, cfg Settings) (verdict string, trust int, reason string) {
	if len(cites) == 0 {
		return "ABSTAIN", 0, "所有检索通道均未返回可用结果，拒绝在零证据下作答。"
	}
	qt := tokenize(query)
	qset := map[string]bool{}
	for _, t := range qt {
		qset[t] = true
	}
	cover := 0
	for _, c := range cites {
		seen := map[string]bool{}
		for _, t := range tokenize(c.Title + " " + c.Snippet) {
			if qset[t] && !seen[t] {
				seen[t] = true
				cover++
			}
		}
	}
	auth := 0
	for _, c := range cites {
		if c.AuthorityLevel >= cfg.AuthorityHigh {
			auth++
		}
	}
	low := 0
	for _, c := range cites {
		if c.AuthorityLevel <= cfg.AuthorityLow {
			low++
		}
	}
	// support: how much of the question the evidence speaks to, weighted by
	// the sources' standing.
	support := float64(cover) / float64(max(1, len(qset)))
	authRatio := float64(auth) / float64(len(cites))
	trust = int(100 * (0.6*support + 0.4*authRatio))
	if trust > 100 {
		trust = 100
	}

	// Refutation and confirmation signals, weighted by who says them: an
	// authority-5 bulletin counts five times a content farm's repeat.
	refute, confirm := 0, 0
	refuteW, confirmW := 0.0, 0.0
	for _, c := range cites {
		text := c.Title + " " + c.Snippet
		w := float64(c.AuthorityLevel) / 5
		if w < 0.2 {
			w = 0.2
		}
		if strings.Contains(text, "辟谣") || strings.Contains(text, "谣言") || strings.Contains(text, "假的") ||
			strings.Contains(text, "不实") || strings.Contains(text, "反转") || strings.Contains(text, "误传") {
			refute++
			refuteW += w
		}
		if strings.Contains(text, "证实") || strings.Contains(text, "通报") || strings.Contains(text, "确认") ||
			strings.Contains(text, "官方") || strings.Contains(text, "发布") {
			confirm++
			confirmW += w
		}
	}
	floor := float64(cfg.VerdictMinSignals) * 0.5

	// The self-RAG gate: a mode that asks for it refuses when there is no
	// authoritative source and nothing that speaks to the claim — coverage
	// alone is a poor signal on a short question, so a refutation or
	// confirmation in the evidence counts as substance too.
	if plan.SelfRAGGate && auth == 0 && refute+confirm == 0 {
		return "ABSTAIN", trust, fmt.Sprintf("检索到 %d 条结果，但权威信源 %d 条、正反证据均缺失，支撑不足，拒绝断言。", len(cites), auth)
	}
	if support < cfg.SupportFloor && refute+confirm == 0 {
		return "ABSTAIN", trust, fmt.Sprintf("检索到 %d 条结果，但与该问题实质相关的内容不足（覆盖度 %.0f%%）。", len(cites), support*100)
	}
	switch {
	// A refutation is the stronger signal: "官方辟谣" also carries official
	// words, so a tie still means the claim was refuted. The verdict needs
	// an authoritative source behind it.
	case refuteW >= floor && refuteW >= confirmW && auth > 0:
		return "REFUTED", trust, fmt.Sprintf("证据中 %d 条指向辟谣/不实（权威加权 %.1f），%d 条含官方/证实性措辞（加权 %.1f），权威信源 %d 条。", refute, refuteW, confirm, confirmW, auth)
	case confirmW >= floor && confirmW > refuteW && auth > 0:
		return "VERIFIED", trust, fmt.Sprintf("证据中 %d 条来自官方/证实性来源（权威加权 %.1f），%d 条指向辟谣（加权 %.1f），权威信源 %d 条。", confirm, confirmW, refute, refuteW, auth)
	case refuteW >= 0.5 && confirmW == 0 && auth > 0:
		return "REFUTED", trust, fmt.Sprintf("证据中 %d 条指向辟谣/不实（权威加权 %.1f），未见证实性来源。", refute, refuteW)
	case refute+confirm > 0 && auth == 0:
		return "UNVERIFIED", trust, fmt.Sprintf("证据 %d 条只有低权威来源在谈正反（权威信源 0 条），不足以判定，待核实。", len(cites))
	default:
		return "UNVERIFIED", trust, fmt.Sprintf("证据 %d 条（权威 %d 条，低质 %d 条），正反信号不明确（辟谣加权 %.1f，证实加权 %.1f）。", len(cites), auth, low, refuteW, confirmW)
	}
}

// claimRows holds the query's claims against what the evidence says.
func claimRows(query string, cites []Citation, verdict string) []ClaimMatrixRow {
	if len(cites) == 0 {
		return []ClaimMatrixRow{}
	}
	claim := coreSubject(query)
	rows := []ClaimMatrixRow{{
		Claim:     claim,
		Fact:      firstRunesOf(cites[0].Snippet, 160),
		Status:    verdict,
		SourceURL: cites[0].URL,
		CiteIndex: 1,
	}}
	// A second row for the strongest authority, when it isn't the first.
	for i, c := range cites {
		if i == 0 || c.AuthorityLevel < 4 {
			continue
		}
		rows = append(rows, ClaimMatrixRow{
			Claim: claim + "（权威印证）", Fact: firstRunesOf(c.Snippet, 160),
			Status: verdict, SourceURL: c.URL, CiteIndex: i + 1,
		})
		break
	}
	return rows
}

// temporalTree builds the time-evolution tree from real dates found in the
// citations' own text.
func temporalTree(cites []Citation, query string) []TemporalTreeNode {
	type node struct {
		t    time.Time
		node TemporalTreeNode
	}
	var ns []node
	for _, c := range cites {
		refs := extractDates(c.Title+" "+c.Snippet, 1)
		if len(refs) == 0 {
			if t, ok := parseDate(c.PublishedAt); ok {
				refs = []dateRef{{Time: t, Stage: t.Format("2006-01-02")}}
			}
		}
		for _, r := range refs {
			ns = append(ns, node{r.Time, TemporalTreeNode{
				Timestamp: r.Time.Format("2006-01-02"),
				Stage:     r.Stage,
				EdgeType:  "MENTIONS",
				Title:     firstRunesOf(c.Title, 48),
				Detail:    firstRunesOf(c.Snippet, 120),
				URL:       c.URL,
			}})
		}
	}
	sort.Slice(ns, func(i, j int) bool { return ns[i].t.After(ns[j].t) })
	var out []TemporalTreeNode
	for i, n := range ns {
		if i == 0 {
			n.node.EdgeType = "LATEST"
		} else {
			n.node.EdgeType = "SUPERSEDES"
		}
		out = append(out, n.node)
		if len(out) >= 8 {
			break
		}
	}
	if len(out) == 0 {
		out = []TemporalTreeNode{{
			Timestamp: time.Now().Format("2006-01-02"),
			Stage:     "检索时点",
			EdgeType:  "OBSERVED",
			Title:     "本次检索未从页面正文提取到明确日期",
			Detail:    "证据页未给出可解析的发布日期；时间树以本次检索时点为准。",
		}}
	}
	return out
}

// debateOf is the pro/con reading of the evidence.
func debateOf(query string, cites []Citation, verdict string, proCon bool) Debate {
	d := Debate{}
	for _, c := range cites {
		if c.AuthorityLevel >= 4 {
			d.Pro = append(d.Pro, fmt.Sprintf("%s（%s，权威 %d）：%s", c.Title, c.Domain, c.AuthorityLevel, firstRunesOf(c.Snippet, 90)))
		}
		if len(d.Pro) >= 3 {
			break
		}
	}
	for _, c := range cites {
		text := c.Title + " " + c.Snippet
		if strings.Contains(text, "辟谣") || strings.Contains(text, "谣言") || strings.Contains(text, "不实") || strings.Contains(text, "假的") {
			d.Con = append(d.Con, fmt.Sprintf("%s（%s）：%s", c.Title, c.Domain, firstRunesOf(c.Snippet, 90)))
		}
		if len(d.Con) >= 3 {
			break
		}
	}
	if len(d.Pro) == 0 {
		d.Pro = []string{"未检索到权威一手信源，正方无有效证据。"}
	}
	if len(d.Con) == 0 {
		d.Con = []string{"未检索到明确的辟谣/反证材料。"}
	}
	d.JudgeSummary = fmt.Sprintf("裁决 %s：基于 %d 条真实检索结果，%s", verdict, len(cites), judgeWords(verdict))
	return d
}

func judgeWords(v string) string {
	switch v {
	case "REFUTED":
		return "证据以辟谣/不实为主，判定原说法不成立。"
	case "VERIFIED":
		return "证据以官方/一手来源为主，判定原说法成立。"
	case "ABSTAIN":
		return "支撑不足，按『查不到敢说不知道』拒答。"
	default:
		return "正反证据均不充分，判定为待核实。"
	}
}

// fusionOf builds the content graph the page draws from the real run. The
// graph flag adds the co-occurrence edges a graph mode asked for; without
// it, the nodes and their metadata are still drawn, but no edge is claimed.
func fusionOf(opt Options, inv Investigation, runs []channelRun, graph bool) FusionNetwork {
	fn := FusionNetwork{
		Origins:    []map[string]string{{"title": "🕵️ Detective Caller", "sub": opt.Query, "badge": inv.ModeID}},
		SubQueries: []map[string]string{},
		Gateways:   []map[string]interface{}{},
	}
	// The plan column is the queries this run actually asked, in order, taken
	// from the trajectory's own params. A run that asked the same query twice
	// shows it once.
	seenQuery := map[string]bool{}
	for _, step := range inv.Trajectory {
		q := strings.TrimSpace(step.Params)
		if q == "" || seenQuery[q] {
			continue
		}
		seenQuery[q] = true
		fn.SubQueries = append(fn.SubQueries, map[string]string{
			"q": firstRunesOf(q, 48), "sub": step.Gateway, "badge": step.ModeID,
		})
		if len(fn.SubQueries) >= 6 {
			break
		}
	}
	primaryID := ""
	for _, r := range runs {
		if r.Err == nil && r.RawHits > 0 {
			primaryID = r.ChannelID
			break
		}
	}
	seen := map[string]bool{}
	for _, r := range runs {
		if seen[r.ChannelID] {
			continue
		}
		seen[r.ChannelID] = true
		lat := fmtMs(r.Latency)
		if r.Err != nil {
			lat = "FAILED"
		}
		fn.Gateways = append(fn.Gateways, map[string]interface{}{
			"name": r.Name, "endpoint": r.ChannelID, "latency": lat,
			"hits":    itoa(r.RawHits) + " hits",
			"primary": r.ChannelID == primaryID, "keyed": keyedChannel(r.ChannelID),
		})
	}
	for i, c := range inv.Citations {
		if i >= 6 {
			break
		}
		fn.EvidenceNodes = append(fn.EvidenceNodes, map[string]interface{}{
			"title": c.Title, "snippet": firstRunesOf(c.Snippet, 120), "url": c.URL,
			"badge":         c.Channel + " · 权威 " + itoa(c.AuthorityLevel),
			"isSpamCluster": c.AuthorityLevel <= 1,
		})
	}
	for i, row := range inv.ClaimMatrix {
		fn.Claims = append(fn.Claims, map[string]string{
			"title": row.Claim, "detail": row.Fact, "status": row.Status, "badge": "#" + itoa(i+1),
			"url": row.SourceURL,
		})
	}
	if graph {
		fn.Edges = cooccurrenceEdges(inv.Citations)
	}
	return fn
}

// cooccurrenceEdges links two evidence nodes that share a token the run
// actually found, from the titles and snippets it returned. The weight is
// the number of shared tokens, so the page can draw strong links thicker.
func cooccurrenceEdges(cites []Citation) []map[string]interface{} {
	limit := len(cites)
	if limit > 6 {
		limit = 6
	}
	terms := make([]map[string]bool, limit)
	for i := 0; i < limit; i++ {
		set := map[string]bool{}
		for _, t := range tokenize(cites[i].Title + " " + cites[i].Snippet) {
			set[t] = true
		}
		terms[i] = set
	}
	var edges []map[string]interface{}
	for i := 0; i < limit; i++ {
		for j := i + 1; j < limit; j++ {
			shared := 0
			label := ""
			for t := range terms[i] {
				if terms[j][t] {
					shared++
					if label == "" {
						label = t
					}
				}
			}
			if shared == 0 {
				continue
			}
			edges = append(edges, map[string]interface{}{
				"from": i, "to": j, "weight": shared, "label": label,
			})
			if len(edges) == 20 {
				return edges
			}
		}
	}
	return edges
}

// summaryOf writes the human briefing sentence.
func summaryOf(query string, inv Investigation, verdict, reason string) string {
	if verdict == "ABSTAIN" {
		return "🛑 " + reason + " 本次共请求 " + itoa(len(inv.Trajectory)) + " 次真实检索。"
	}
	line := fmt.Sprintf("🔎 已对「%s」执行 %s：真实请求 %d 次，召回 %d 条引用（其中权威信源 %d 条），去重折叠 %d 条，判定 %s。%s",
		firstRunesOf(query, 40), inv.ModeID+" "+inv.ModeName, len(inv.Trajectory), len(inv.Citations),
		countAuthority(inv.Citations), inv.CollapsedSpamCount, verdict, reason)
	if inv.Answer.Status == "ok" && inv.Answer.Text != "" {
		return "✅ 答案：" + inv.Answer.Text + "（" + inv.Answer.Model + " 依据 " +
			itoa(len(inv.Answer.CiteIndexes)) + " 条引用合成）\n\n" + line
	}
	if inv.Answer.Status == "insufficient" {
		return "⚠️ 证据不足以合成答案。\n\n" + line
	}
	return line
}

// briefingOf is the human-readable markdown report.
func briefingOf(inv Investigation) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# 🔎 %s\n\n", firstRunesOf(inv.Query, 60))
	fmt.Fprintf(&b, "- **判定**: `%s`（信任分 %d/100）\n", inv.Verdict, inv.TrustScore)
	fmt.Fprintf(&b, "- **模式**: %s %s\n", inv.ModeID, inv.ModeName)
	fmt.Fprintf(&b, "- **引擎**: %s\n", inv.PrimaryEngine)
	fmt.Fprintf(&b, "- **耗时**: %d ms，真实请求 %d 次\n\n", inv.TotalLatencyMs, len(inv.Trajectory))
	if inv.AbstainReason != "" {
		fmt.Fprintf(&b, "> ⚠️ %s\n\n", inv.AbstainReason)
	}
	if inv.Answer.Status == "ok" && inv.Answer.Text != "" {
		fmt.Fprintf(&b, "## 答案\n\n%s\n\n_由 %s 依据 %d 条引用合成；证据见下。_\n\n", inv.Answer.Text, inv.Answer.Model, len(inv.Answer.CiteIndexes))
	} else if inv.Answer.Status == "insufficient" {
		b.WriteString("## 答案\n\n证据不足以合成答案。\n\n")
	}
	b.WriteString("## 证据\n\n")
	for i, c := range inv.Citations {
		if i >= 8 {
			break
		}
		fmt.Fprintf(&b, "%d. [%s](%s) — %s · 权威 %d\n   %s\n\n", i+1, c.Title, c.URL, c.Domain, c.AuthorityLevel, firstRunesOf(c.Snippet, 140))
	}
	return b.String()
}

// envelopeOf is the compact XML an agent reads instead of the briefing.
func envelopeOf(inv Investigation) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<magpie_evidence_envelope version="5.0" mode="%s" verdict="%s" trust="%d" latency_ms="%d">`, inv.ModeID, inv.Verdict, inv.TrustScore, inv.TotalLatencyMs)
	b.WriteString("\n  <evidence>\n")
	for i, c := range inv.Citations {
		if i >= 8 {
			break
		}
		fmt.Fprintf(&b, "    <cite n=\"%d\" authority=\"%d\" domain=\"%s\"><title>%s</title><url>%s</url></cite>\n",
			i+1, c.AuthorityLevel, xmlEscape(c.Domain), xmlEscape(c.Title), xmlEscape(c.URL))
	}
	b.WriteString("  </evidence>\n")
	if inv.Answer.Status == "ok" && inv.Answer.Text != "" {
		idx := make([]string, 0, len(inv.Answer.CiteIndexes))
		for _, n := range inv.Answer.CiteIndexes {
			idx = append(idx, itoa(n+1))
		}
		fmt.Fprintf(&b, "  <answer model=\"%s\" cites=\"%s\">%s</answer>\n", xmlEscape(inv.Answer.Model), strings.Join(idx, ","), xmlEscape(inv.Answer.Text))
	}
	fmt.Fprintf(&b, "  <verdict reason=\"%s\"/>\n", xmlEscape(inv.AbstainReason))
	b.WriteString("</magpie_evidence_envelope>")
	return b.String()
}

// xmlEscape keeps an agent's XML well-formed.
func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}

func countAuthority(cs []Citation) int {
	n := 0
	for _, c := range cs {
		if c.AuthorityLevel >= 4 {
			n++
		}
	}
	return n
}

func joinCites(cs []Citation) string {
	var b strings.Builder
	for _, c := range cs {
		b.WriteString(c.Title)
		b.WriteString(" ")
		b.WriteString(c.Snippet)
		b.WriteString(" ")
	}
	return b.String()
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
