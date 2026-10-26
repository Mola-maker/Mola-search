package searchdet

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestTokenizeCJKBigrams(t *testing.T) {
	toks := tokenize("北京冰雹 hail storm 2026")
	want := []string{"北京", "京冰", "冰雹", "hail", "storm", "2026"}
	for _, w := range want {
		found := false
		for _, got := range toks {
			if got == w {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("tokenize missing %q in %v", w, toks)
		}
	}
}

func TestSimHashFoldsNearDuplicates(t *testing.T) {
	a := "北京朝阳公园今天下午突降鸡蛋大冰雹砸毁数十辆汽车"
	b := "北京朝阳公园今天下午突降鸡蛋大冰雹砸毁数十辆汽车！"
	c := "央行宣布下调存款准备金率0.5个百分点"
	if !nearDup(simhash64(a), simhash64(b)) {
		t.Fatal("near-identical texts should fold")
	}
	if nearDup(simhash64(a), simhash64(c)) {
		t.Fatal("unrelated texts should not fold")
	}
}

func TestRedactMasksAndBlocks(t *testing.T) {
	const id = "110105199003078828"
	a := Redact("查一下身份证" + id + "的体检指标")
	if a.MaskedCount != 1 {
		t.Fatalf("masked %d, want 1 (%v)", a.MaskedCount, a.Placeholders)
	}
	if strings.Contains(a.MaskedQuery, id) {
		t.Fatalf("the ID card survived: %s", a.MaskedQuery)
	}
	b := Redact("帮我人肉开盒查某人家庭住址")
	if b.ComplianceStatus != "BLOCKED_REDLINE" {
		t.Fatalf("red line not blocked: %s", b.ComplianceStatus)
	}
}

func TestPlansDifferByMode(t *testing.T) {
	m01 := planFor("M01", "q")
	m13 := planFor("M13", "q")
	m25 := planFor("M25", "q")
	m17 := planFor("M17", "q")
	if len(m01.Queries) != 1 || m01.ProCon || len(m01.Multimodal) != 0 {
		t.Fatalf("M01 should be a plain single search: %+v", m01)
	}
	if !m13.ProCon || !m13.SelfRAGGate {
		t.Fatal("M13 should argue both sides and gate on support")
	}
	if len(m25.Multimodal) == 0 || m25.Multimodal[0] != "weather" {
		t.Fatalf("M25 should ask for the weather check: %+v", m25.Multimodal)
	}
	if !m17.Temporal {
		t.Fatal("M17 should build the temporal tree")
	}
}

func TestAssessAbstainsWithoutEvidence(t *testing.T) {
	v, trust, reason := assess("某件没人报道的事", nil, modePlan{SelfRAGGate: true}, DefaultSettings())
	if v != "ABSTAIN" || trust != 0 || reason == "" {
		t.Fatalf("empty evidence: %s %d %q", v, trust, reason)
	}
}

func TestAssessRefutesWhenEvidenceSaysSo(t *testing.T) {
	cites := []Citation{
		{Title: "官方辟谣：网传说法不实", Snippet: "经核实该消息为谣言，系旧图新炒。", AuthorityLevel: 5, Domain: "piyao.org.cn"},
		{Title: "辟谣平台通报", Snippet: "该说法已被证实为假的。", AuthorityLevel: 5, Domain: "cma.gov.cn"},
	}
	v, trust, _ := assess("网传北京冰雹砸车", cites, modePlan{SelfRAGGate: true}, DefaultSettings())
	if v != "REFUTED" {
		t.Fatalf("verdict %s, want REFUTED (trust %d)", v, trust)
	}
	if trust <= 0 {
		t.Fatal("trust should be above zero with authority sources")
	}
}

// stubChannels stands in for the network: it answers each query with a
// fixed citation and records that it ran.
func stubChannels(seen *atomic.Int64) func(context.Context, string, Keys, int, map[string]bool) []runResult {
	return func(ctx context.Context, query string, k Keys, n int, only map[string]bool) []runResult {
		seen.Add(1)
		return []runResult{{
			run: channelRun{Name: "Stub", ChannelID: "stub", Query: query, Latency: 3 * time.Millisecond, RawHits: 1},
			cite: []Citation{{
				Title: "Stub 结果：" + query, URL: "https://example.gov.cn/a", Snippet: query + " 的官方说明",
				Channel: "stub", AuthorityLevel: 5,
			}},
		}}
	}
}

func TestInvestigateUsesChannelsAndRecordsTrajectory(t *testing.T) {
	seen := &atomic.Int64{}
	e := NewEngine()
	e.channelRunner = stubChannels(seen)
	e.keysFn = func() Keys { return Keys{} }
	e.nowFn = func() time.Time { return time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC) }

	inv := e.Investigate(context.Background(), Options{Query: "某地今天有冰雹吗", ModeID: "M13"})
	if seen.Load() == 0 {
		t.Fatal("no channel ran")
	}
	if len(inv.Citations) == 0 {
		t.Fatal("no citations")
	}
	if len(inv.Trajectory) == 0 {
		t.Fatal("no trajectory recorded")
	}
	if inv.TraceID == "" || inv.GeneratedAt == "" {
		t.Fatal("trace identity missing")
	}
	// M13 asks its own queries plus the counter-side ones.
	if seen.Load() < 4 {
		t.Fatalf("M13 ran %d channel calls, want at least 4", seen.Load())
	}
	if inv.ModeName == "" {
		t.Fatal("mode name not resolved from the catalog")
	}
}

// newTestEngine builds an engine over an empty data dir, so a settings file
// left by a real run cannot decide what a test sees.
func newTestEngine(t *testing.T) *Engine {
	t.Helper()
	t.Setenv("MOLASEARCH_DATA", t.TempDir())
	return NewEngine()
}

// TestUnpluggedProviderFailsOver is the decoupling contract: turning one
// provider off must not stop the run. The other providers still serve it,
// and the verdict comes from their evidence.
func TestUnpluggedProviderFailsOver(t *testing.T) {
	seen := &atomic.Int64{}
	e := newTestEngine(t)
	e.channelRunner = stubChannels(seen)
	e.keysFn = func() Keys { return Keys{} }
	if err := e.SetChannelEnabled("tencent", false); err != nil {
		t.Fatal(err)
	}
	if !e.Settings().Disabled("tencent") {
		t.Fatal("unplugging did not disable the provider")
	}
	if e.allowedChannels(nil)["tencent"] {
		t.Fatal("a disabled provider is still in the run's allowlist")
	}
	allowed := 0
	for _, on := range e.allowedChannels(nil) {
		if on {
			allowed++
		}
	}
	if allowed == 0 {
		t.Fatal("disabling one provider left no provider to fail over to")
	}
	inv := e.Investigate(context.Background(), Options{Query: "今天北京天气", ModeID: "M01"})
	if seen.Load() == 0 {
		t.Fatal("the run did not ask any provider")
	}
	if inv.Verdict == "ABSTAIN" && len(inv.Citations) == 0 {
		t.Fatal("a disabled provider aborted the whole run")
	}
}

// TestBreakerOpensAndRecovers holds the per-channel breaker to its promise:
// consecutive failures take a provider out of the run for the cooldown, and
// once the cooldown passes it is tried again.
func TestBreakerOpensAndRecovers(t *testing.T) {
	e := newTestEngine(t)
	st := e.Settings()
	st.BreakerFails = 1
	if err := e.SetSettings(st); err != nil {
		t.Fatal(err)
	}
	e.recordHealth([]runResult{{
		run: channelRun{Name: "Tavily", ChannelID: "tavily", Err: errors.New("down")},
	}}, st)
	if !e.breakerOpen("tavily") {
		t.Fatal("breaker did not open after the configured failures")
	}
	if e.allowedChannels(nil)["tavily"] {
		t.Fatal("an open breaker's provider is still in the run's allowlist")
	}
	// The cooldown is a clock, not a sleep: move the deadline into the past.
	e.healthMu.Lock()
	e.health["tavily"].BreakerOpenUntil = time.Now().Add(-time.Second).Format(time.RFC3339)
	e.healthMu.Unlock()
	if e.breakerOpen("tavily") {
		t.Fatal("breaker stayed open past its cooldown")
	}
	if !e.allowedChannels(nil)["tavily"] {
		t.Fatal("provider not retried after the cooldown")
	}
}

// TestGapRoundsAskWhatWasNotAnswered is the AREX switch's observable
// behavior: a round asks the query's missing terms, and the settings cap
// can turn the loop off.
func TestGapRoundsAskWhatWasNotAnswered(t *testing.T) {
	var (
		mu      sync.Mutex
		queries []string
		calls   int
	)
	e := NewEngine()
	e.channelRunner = func(ctx context.Context, query string, k Keys, n int, only map[string]bool) []runResult {
		mu.Lock()
		queries = append(queries, query)
		calls++
		i := calls
		mu.Unlock()
		return []runResult{{
			run: channelRun{Name: "Stub", ChannelID: "stub", Query: query, RawHits: 1},
			cite: []Citation{{
				Title: "冰雹天气报道", URL: fmt.Sprintf("https://example.com/%d", i),
				Snippet: "冰雹 情况", Channel: "stub", AuthorityLevel: 3,
			}},
		}}
	}
	inv := Investigation{ModeID: "M09"}
	cfg := DefaultSettings()
	cfg.GapRoundsCap = 2
	_, steps := e.gapRounds(context.Background(), &inv, Options{Query: "北京冰雹保险理赔"},
		Keys{}, modePlan{GapRounds: 2}, cfg,
		[]Citation{{Title: "冰雹天气报道", URL: "https://example.com/a", Snippet: "冰雹 情况"}},
		time.Now(), "test", nil)
	if len(steps) == 0 || calls == 0 {
		t.Fatal("gap rounds asked nothing")
	}
	askedGap := false
	for _, q := range queries {
		if strings.Contains(q, "保险") || strings.Contains(q, "理赔") {
			askedGap = true
		}
	}
	if !askedGap {
		t.Fatalf("gap queries did not name the unanswered terms: %v", queries)
	}
	if calls > cfg.GapRoundsCap {
		t.Fatalf("settings cap ignored: %d calls > %d", calls, cfg.GapRoundsCap)
	}

	calls = 0
	cfg.GapRoundsCap = 0
	e.gapRounds(context.Background(), &inv, Options{Query: "北京冰雹保险理赔"},
		Keys{}, modePlan{GapRounds: 2}, cfg, nil, time.Now(), "test", nil)
	if calls != 0 {
		t.Fatalf("gapRoundsCap=0 still ran %d rounds", calls)
	}
}

// TestNoveltyStopEndsHopWhenNothingNew is the RAAC switch's observable
// behavior: once a round adds no URL, the next round is not run.
func TestNoveltyStopEndsHopWhenNothingNew(t *testing.T) {
	mk := func() (*Engine, *atomic.Int64) {
		seen := &atomic.Int64{}
		e := NewEngine()
		e.channelRunner = func(ctx context.Context, query string, k Keys, n int, only map[string]bool) []runResult {
			seen.Add(1)
			return []runResult{{
				run: channelRun{Name: "Stub", ChannelID: "stub", Query: query, RawHits: 1},
				cite: []Citation{{
					Title: "北京冰雹 冰雹 保险 保险", URL: "https://example.com/same",
					Snippet: "北京冰雹 保险", Channel: "stub", AuthorityLevel: 3,
				}},
			}}
		}
		return e, seen
	}
	cites := []Citation{{
		Title: "北京冰雹 冰雹 保险 保险", URL: "https://example.com/same",
		Snippet: "北京冰雹 保险", Channel: "stub", AuthorityLevel: 3,
	}}
	e, seen := mk()
	e.hop(context.Background(), &Investigation{ModeID: "M10", Citations: append([]Citation{}, cites...)},
		Options{Query: "北京冰雹保险"}, Keys{}, modePlan{Hops: 3, NoveltyStop: true}, DefaultSettings(), defaultRedlineTerms, nil)
	stopped := seen.Load()

	e2, seen2 := mk()
	e2.hop(context.Background(), &Investigation{ModeID: "M10", Citations: append([]Citation{}, cites...)},
		Options{Query: "北京冰雹保险"}, Keys{}, modePlan{Hops: 3}, DefaultSettings(), defaultRedlineTerms, nil)
	full := seen2.Load()

	if stopped == 0 {
		t.Fatal("hop ran no round at all")
	}
	if stopped >= full {
		t.Fatalf("NoveltyStop did not shorten the loop: stopped=%d full=%d", stopped, full)
	}
}

// TestGraphEdgesOnlyWhenModeAsks is the Graph switch's observable behavior:
// a graph mode gets co-occurrence edges, a non-graph mode gets none.
func TestGraphEdgesOnlyWhenModeAsks(t *testing.T) {
	inv := Investigation{
		ModeID: "M07",
		Citations: []Citation{
			{Title: "北京冰雹 保险", Snippet: "冰雹 理赔", Channel: "bing", AuthorityLevel: 3},
			{Title: "北京冰雹 报道", Snippet: "冰雹 天气", Channel: "ddg", AuthorityLevel: 3},
		},
	}
	with := fusionOf(Options{Query: "北京冰雹"}, inv, nil, true)
	without := fusionOf(Options{Query: "北京冰雹"}, inv, nil, false)
	if len(with.Edges) == 0 {
		t.Fatal("graph mode got no co-occurrence edges")
	}
	if len(without.Edges) != 0 {
		t.Fatal("non-graph mode claimed edges")
	}
}

// TestPrimaryEngineNamesOnlyProvidersThatAnswered keeps the report from
// crediting a vendor that returned nothing.
func TestPrimaryEngineNamesOnlyProvidersThatAnswered(t *testing.T) {
	got := primaryEngineOf([]channelRun{
		{Name: "Bing", ChannelID: "bing", RawHits: 2},
		{Name: "Tavily", ChannelID: "tavily", RawHits: 0},
		{Name: "DDG", ChannelID: "ddg", RawHits: 3},
	})
	if !strings.Contains(got, "Bing") || !strings.Contains(got, "DDG") {
		t.Fatalf("providers that answered are missing: %q", got)
	}
	if strings.Contains(got, "Tavily") {
		t.Fatalf("credited a provider with zero hits: %q", got)
	}
}

// TestSettingsRoundTripAndValidation holds the page's knobs: a valid set
// survives a save/load, an out-of-range one is refused, and a disabled
// provider is remembered.
func TestSettingsRoundTripAndValidation(t *testing.T) {
	t.Setenv("MOLASEARCH_DATA", t.TempDir())
	want := DefaultSettings()
	want.TopK = 12
	want.MergeCap = 24
	want.BreakerFails = 5
	want.DisabledChannels = []string{"tavily"}
	if err := SaveSettings(want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got.TopK != 12 || got.MergeCap != 24 || got.BreakerFails != 5 || !got.Disabled("tavily") {
		t.Fatalf("settings did not round-trip: %+v", got)
	}
	bad := want
	bad.TopK = 0
	if err := SaveSettings(bad); err == nil {
		t.Fatal("out-of-range topK was accepted")
	}
	bad = want
	bad.DisabledChannels = []string{"no-such-provider"}
	if err := SaveSettings(bad); err == nil {
		t.Fatal("unknown provider id was accepted")
	}
}

// TestSettingsKeepSavedZeroes is the gate a saved 0 depends on: folding off,
// no support floor and no gap or novelty rounds are real settings, so they
// must come back as 0 instead of reverting to the defaults. A key the file
// leaves out still takes its default.
func TestSettingsKeepSavedZeroes(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MOLASEARCH_DATA", dir)
	want := DefaultSettings()
	want.FoldGamma = 0
	want.SupportFloor = 0
	want.GapRoundsCap = 0
	want.NoveltyRoundsCap = 0
	want.TopK = 7
	want.MergeCap = 7
	if err := SaveSettings(want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got.FoldGamma != 0 || got.SupportFloor != 0 || got.GapRoundsCap != 0 || got.NoveltyRoundsCap != 0 {
		t.Fatalf("a saved 0 reverted to its default: %+v", got)
	}
	if got.TopK != 7 || got.MergeCap != 7 {
		t.Fatalf("saved values did not round-trip: %+v", got)
	}

	// A file that names only one gate leaves every other gate at its default.
	if err := os.WriteFile(SettingsFile(), []byte(`{"topK": 9, "mergeCap": 12}`), 0o644); err != nil {
		t.Fatal(err)
	}
	partial, err := LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	def := DefaultSettings()
	if partial.TopK != 9 || partial.MergeCap != 12 {
		t.Fatalf("named keys were not applied: %+v", partial)
	}
	if partial.FoldGamma != def.FoldGamma || partial.SupportFloor != def.SupportFloor ||
		partial.GapRoundsCap != def.GapRoundsCap || partial.NoveltyRoundsCap != def.NoveltyRoundsCap {
		t.Fatalf("keys the file left out did not take their default: %+v", partial)
	}
}

func TestFoldResultsKeepsStrongest(t *testing.T) {
	now := time.Now()
	cites := []Citation{
		{Title: "同一件事的报道", Snippet: "完全相同的内容描述", AuthorityLevel: 1, Domain: "farm.example"},
		{Title: "同一件事的报道！", Snippet: "完全相同的内容描述", AuthorityLevel: 5, Domain: "gov.cn"},
	}
	kept, folded := foldResults(cites, now, 0.35)
	if folded != 1 || len(kept) != 1 {
		t.Fatalf("folded %d kept %d", folded, len(kept))
	}
	if kept[0].AuthorityLevel != 5 {
		t.Fatalf("kept the weaker source: %+v", kept[0])
	}
}

func TestCatalogHasThirtyModes(t *testing.T) {
	modes := Catalog()
	if len(modes) != 30 {
		t.Fatalf("catalog has %d modes, want 30", len(modes))
	}
	if _, ok := ModeOf("M30"); !ok {
		t.Fatal("M30 missing")
	}
	if modes[0].NameZh == "" || modes[0].Topology == "" {
		t.Fatalf("catalog fields not mapped: %+v", modes[0])
	}
}

func TestImageCardComputesRealFingerprints(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 16, 12))
	for y := 0; y < 12; y++ {
		for x := 0; x < 16; x++ {
			img.Set(x, y, color.RGBA{uint8(x * 16), uint8(y * 20), 64, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	card, err := imageCardFromBytes("verify.png", buf.Bytes())
	if err != nil {
		t.Fatalf("image card: %v", err)
	}
	detail := card.TechDetail + " " + card.Summary
	if !strings.Contains(detail, "dHash") || !strings.Contains(detail, "aHash") {
		t.Fatalf("no perceptual hashes in the card: %s", detail)
	}
	if !strings.Contains(detail, "16") || !strings.Contains(detail, "12") {
		t.Fatalf("dimensions missing: %s", detail)
	}
}

// planSignature is what a mode actually does, without its prose: the
// questions it asks, how many hops and rounds, and every switch the engine
// reads. Two modes with the same signature run the same pipeline.
func planSignature(p modePlan) string {
	var b strings.Builder
	for _, q := range p.Queries {
		b.WriteString(q.Role)
		b.WriteByte(':')
		b.WriteString(q.Q)
		b.WriteByte('|')
	}
	return fmt.Sprintf("%s h=%d gap=%d procon=%t novel=%t gate=%t temporal=%t graph=%t auth=%t fresh=%t nofold=%t nopersist=%t hyde=%t snow=%t mm=%v",
		b.String(), p.Hops, p.GapRounds, p.ProCon, p.NoveltyStop, p.SelfRAGGate,
		p.Temporal, p.Graph, p.AuthBoost, p.FreshOnly, p.NoFold, p.NoPersist,
		p.HyDE, p.Snowball, p.Multimodal)
}

// TestEveryModePlansDifferentWork holds the catalog's promise: 30 modes are
// 30 pipelines. A mode that only changed its badge would collide here.
func TestEveryModePlansDifferentWork(t *testing.T) {
	seen := map[string]string{}
	for i := 1; i <= 30; i++ {
		id := fmt.Sprintf("M%02d", i)
		sig := planSignature(planFor(id, "同一查询"))
		if prev, ok := seen[sig]; ok {
			t.Errorf("%s and %s plan the same work: %s", prev, id, sig)
		}
		seen[sig] = id
	}
}

// TestModePlansMatchTheCatalogTopologies pins the modes whose algorithm the
// plan names: the catalog says citation snowball, falsification and
// zero-retention, and these are the switches that make them true.
func TestModePlansMatchTheCatalogTopologies(t *testing.T) {
	if p := planFor("M08", "q"); !p.Snowball {
		t.Error("M08 (citation snowball) does not follow the sources' domains")
	}
	if p := planFor("M16", "q"); !p.ProCon {
		t.Error("M16 (Popper falsification) asks no disconfirming questions")
	}
	if p := planFor("M21", "q"); !p.NoPersist {
		t.Error("M21 (privacy) keeps a record")
	}
	if p := planFor("M24", "q"); !p.NoFold || !p.Graph {
		t.Error("M24 (federated) does not keep the backends apart")
	}
	if p := planFor("M26", "q"); len(p.Multimodal) == 0 || p.Multimodal[0] != "image" {
		t.Error("M26 (image provenance) asks for no image check")
	}
	if p := planFor("M03", "q"); !p.HyDE {
		t.Error("M03 (HyDE) drafts no hypothetical answer")
	}
}

// TestBriefingQuotesTheRunLatency: the Markdown briefing and the agent
// envelope are built once the run's latency is known. Before that they
// always said "耗时: 0 ms" while the page header showed the real number.
func TestBriefingQuotesTheRunLatency(t *testing.T) {
	e := newTestEngine(t)
	e.keysFn = func() Keys { return Keys{} }
	e.channelRunner = func(context.Context, string, Keys, int, map[string]bool) []runResult {
		time.Sleep(20 * time.Millisecond)
		return []runResult{{
			run:  channelRun{Name: "Stub", ChannelID: "stub", RawHits: 1},
			cite: []Citation{{Title: "北京冰雹天气实况", URL: "https://weather.example.org/a", Snippet: "冰雹橙色预警", Channel: "stub", AuthorityLevel: 5}},
		}}
	}
	inv := e.Investigate(context.Background(), Options{Query: "北京冰雹天气", ModeID: "M01"})
	if inv.TotalLatencyMs < 10 {
		t.Fatalf("latency = %d ms, the run slept 20 ms", inv.TotalLatencyMs)
	}
	want := fmt.Sprintf("- **耗时**: %d ms", inv.TotalLatencyMs)
	if !strings.Contains(inv.MarkdownBriefing, want) {
		t.Fatalf("briefing does not quote the run latency (%q): %s", want, inv.MarkdownBriefing)
	}
	wantEnv := fmt.Sprintf(`latency_ms="%d"`, inv.TotalLatencyMs)
	if !strings.Contains(inv.AgentEnvelopeXML, wantEnv) {
		t.Fatalf("envelope does not quote the run latency (%q): %s", wantEnv, inv.AgentEnvelopeXML)
	}
}
