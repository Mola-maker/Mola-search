package searchdet

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

const (
	testPhone = "13912345678"
	testEmail = "alice@corp.cn"
)

// TestOutputGuardDropsRedlineAndMasksPII is the result-side contract: a
// citation that carries a red-line term never reaches the page or an agent,
// and personal data in the citations that are kept is masked in the
// citation, the Markdown briefing and the agent envelope alike.
func TestOutputGuardDropsRedlineAndMasksPII(t *testing.T) {
	e := newTestEngine(t)
	e.keysFn = func() Keys { return Keys{} }
	e.channelRunner = func(context.Context, string, Keys, int, map[string]bool) []runResult {
		return []runResult{{
			run: channelRun{Name: "Stub", ChannelID: "stub", RawHits: 3},
			cite: []Citation{
				{
					Title: "联系 " + testPhone + " 咨询冰雹理赔", URL: "https://claims.example.org/a",
					Snippet: "邮箱 " + testEmail + " 是本次冰雹理赔的唯一联系方式。", Channel: "stub",
					Domain: "claims.example.org", SourceName: "理赔中心", AuthorityLevel: 4,
				},
				{
					Title: "私生活曝光：某地冰雹夜的传闻", URL: "https://gossip.example.net/b",
					Snippet: "这条结果命中红线，整条丢弃。", Channel: "stub",
					Domain: "gossip.example.net", SourceName: "八卦站", AuthorityLevel: 1,
				},
				{
					Title: "北京冰雹天气实况", URL: "https://weather.example.org/c",
					Snippet: "气象台发布冰雹橙色预警。", Channel: "stub",
					Domain: "weather.example.org", SourceName: "气象台", AuthorityLevel: 5,
				},
			},
		}}
	}

	inv := e.Investigate(context.Background(), Options{Query: "北京冰雹理赔进度", ModeID: "M01"})
	if inv.OutputGuard.Policy != OutputGuardPolicy {
		t.Fatalf("policy = %q", inv.OutputGuard.Policy)
	}
	if inv.OutputGuard.Scanned != 3 || inv.OutputGuard.Dropped != 1 || inv.OutputGuard.Kept != 2 {
		t.Fatalf("guard = %+v", inv.OutputGuard)
	}
	if inv.OutputGuard.RedlineHits["私生活"] != 1 {
		t.Fatalf("red-line term not recorded: %+v", inv.OutputGuard.RedlineHits)
	}
	if len(inv.Citations) != 2 {
		t.Fatalf("citations = %d, want 2", len(inv.Citations))
	}
	for _, c := range inv.Citations {
		if strings.Contains(c.Title+c.Snippet, testPhone) || strings.Contains(c.Title+c.Snippet, testEmail) {
			t.Fatalf("PII survived in a citation: %+v", c)
		}
	}
	joined := ""
	for _, c := range inv.Citations {
		joined += c.Title + " " + c.Snippet + " "
	}
	if !strings.Contains(joined, "[PHONE]") || !strings.Contains(joined, "[EMAIL]") {
		t.Fatalf("markers missing: %s", joined)
	}
	for _, text := range []string{inv.MarkdownBriefing, inv.AgentEnvelopeXML, inv.ExecutiveSummary} {
		if strings.Contains(text, testPhone) || strings.Contains(text, testEmail) {
			t.Fatalf("PII survived in a derived view: %s", text)
		}
	}
	raw, err := json.Marshal(inv)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), testPhone) || strings.Contains(string(raw), testEmail) {
		t.Fatalf("PII survived in the MCP payload")
	}
	if inv.OutputGuard.Kinds["PHONE"] == 0 || inv.OutputGuard.Kinds["EMAIL"] == 0 {
		t.Fatalf("kinds not counted: %+v", inv.OutputGuard.Kinds)
	}
}

// TestOutputGuardAllDroppedAbstains: when the only evidence hits a red line,
// the run says so instead of concluding from what was removed.
func TestOutputGuardAllDroppedAbstains(t *testing.T) {
	e := newTestEngine(t)
	e.keysFn = func() Keys { return Keys{} }
	e.channelRunner = func(context.Context, string, Keys, int, map[string]bool) []runResult {
		return []runResult{{
			run: channelRun{Name: "Stub", ChannelID: "stub", RawHits: 1},
			cite: []Citation{{
				Title: "私生活内幕", URL: "https://gossip.example.net/a",
				Snippet: "私生活 内幕 传闻", Channel: "stub", AuthorityLevel: 3,
			}},
		}}
	}
	inv := e.Investigate(context.Background(), Options{Query: "北京今天天气", ModeID: "M01"})
	if inv.Verdict != "ABSTAIN" {
		t.Fatalf("verdict = %s, want ABSTAIN", inv.Verdict)
	}
	if len(inv.Citations) != 0 || inv.OutputGuard.Dropped != 1 || inv.OutputGuard.Kept != 0 {
		t.Fatalf("citations=%d guard=%+v", len(inv.Citations), inv.OutputGuard)
	}
	if !strings.Contains(inv.AbstainReason, "输出侧合规过滤") {
		t.Fatalf("abstain reason does not name the filter: %s", inv.AbstainReason)
	}
}

// TestInvestigationSendsOnlyMaskedQuery: every query that leaves the machine
// is the masked one, including the pro/con counter-queries, the HyDE draft,
// the snowball and the gap rounds that are built from the original text.
func TestInvestigationSendsOnlyMaskedQuery(t *testing.T) {
	e := newTestEngine(t)
	e.keysFn = func() Keys { return Keys{} }
	var asked []string
	var mu sync.Mutex
	e.channelRunner = func(_ context.Context, query string, _ Keys, _ int, _ map[string]bool) []runResult {
		mu.Lock()
		asked = append(asked, query)
		mu.Unlock()
		return []runResult{{
			run:  channelRun{Name: "Stub", ChannelID: "stub", RawHits: 1},
			cite: []Citation{{Title: "结果", URL: "https://example.gov.cn/a", Snippet: "天气", Channel: "stub", AuthorityLevel: 4}},
		}}
	}
	inv := e.Investigate(context.Background(), Options{Query: "联系 " + testPhone + " 查北京冰雹", ModeID: "M13"})
	if inv.RedactAudit.MaskedCount == 0 {
		t.Fatal("the phone number was not masked at the input gate")
	}
	if len(asked) == 0 {
		t.Fatal("no channel ran")
	}
	for _, q := range asked {
		if strings.Contains(q, testPhone) {
			t.Fatalf("the raw phone number left the machine: %q", q)
		}
	}
}

// safeSearchTerm is the entity path's guard: a scraped entity that carries a
// phone number or a red-line term is not searched.
func TestSafeSearchTerm(t *testing.T) {
	terms := defaultRedlineTerms
	if got := safeSearchTerm("北京冰雹", terms); got != "北京冰雹" {
		t.Fatalf("clean entity = %q", got)
	}
	if got := safeSearchTerm("联系 "+testPhone, terms); got != "" {
		t.Fatalf("an entity with a phone number was searched: %q", got)
	}
	if got := safeSearchTerm("私生活", terms); got != "" {
		t.Fatalf("a red-line entity was searched: %q", got)
	}
}

// TestGuardDerivedMasksEveryStringField holds the pass over the views that
// exist after the citations were guarded: no string field of an
// Investigation may carry a phone or an email once guardOutput has run.
func TestGuardDerivedMasksEveryStringField(t *testing.T) {
	inv := Investigation{
		Query:            "北京冰雹",
		ModeID:           "M07",
		ExecutiveSummary: testPhone,
		ModeNote:         testEmail,
		MarkdownBriefing: "电话 " + testPhone,
		AgentEnvelopeXML: `<x mail="` + testEmail + `"/>`,
		ClaimMatrix:      []ClaimMatrixRow{{Claim: testPhone, Fact: testEmail, Status: testPhone, SourceURL: "https://claims.test/?u=" + testEmail}},
		TemporalTree:     []TemporalTreeNode{{Timestamp: testPhone, Stage: testEmail, EdgeType: testPhone, Title: testEmail, Detail: testPhone, URL: "https://t.test/" + testEmail}},
		Debate:           Debate{Pro: []string{testPhone}, Con: []string{testEmail}, JudgeSummary: testPhone},
		Trajectory: []TrajectoryStep{{
			OriginAgent: testPhone, ModeID: testEmail, Gateway: testPhone, Params: testEmail,
			TimingBreakdown: testPhone, ReturnSummary: testEmail, TopURL: "https://u.test/" + testPhone, TopDomain: testEmail,
		}},
		FusionNetwork: FusionNetwork{
			Origins:       []map[string]string{{"agent": testPhone}},
			SubQueries:    []map[string]string{{"q": testEmail}},
			Claims:        []map[string]string{{"claim": testPhone}},
			Gateways:      []map[string]interface{}{{"name": testPhone}},
			EvidenceNodes: []map[string]interface{}{{"snippet": map[string]interface{}{"nested": testEmail}}},
			Edges:         []map[string]interface{}{{"label": testPhone}},
		},
		AbstainReason: testPhone,
	}
	audit := &OutputGuardAudit{Policy: OutputGuardPolicy}
	guardOutput(&inv, []string{"私生活"}, audit)
	raw, err := json.Marshal(inv)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{testPhone, testEmail} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("%s survived guardOutput: %s", secret, raw)
		}
	}
}

// TestSanitizeCardMasksPersonalData is the upload path: the card returned
// outside an investigation gets the same field pass as a card inside one.
func TestSanitizeCardMasksPersonalData(t *testing.T) {
	card := MultimodalCard{
		Icon: "🖼️", Title: "图片：" + testPhone, Provider: "本地解码", Status: "✅",
		Badge: "16x12", Summary: "相机 " + testEmail, TechDetail: "dHash=abc mail=" + testEmail,
		SourceURL: "https://example.test/?u=" + testPhone,
		Keyframes: []KeyframeItem{{TS: "00:01", Desc: "联系 " + testPhone}},
	}
	got := SanitizeCard(card)
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{testPhone, testEmail} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("%s survived SanitizeCard: %s", secret, raw)
		}
	}
	if !strings.Contains(got.Summary, "[EMAIL]") || !strings.Contains(got.Title, "[PHONE]") {
		t.Fatalf("markers missing: %+v", got)
	}
}
