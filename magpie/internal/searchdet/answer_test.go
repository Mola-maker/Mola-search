package searchdet

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// answerServer stands in for the model endpoint: it records the request and
// replies with the body the test chose.
func answerServer(t *testing.T, body string) (*httptest.Server, *map[string]any) {
	t.Helper()
	seen := &map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			t.Errorf("the answer call carried no key")
		}
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		*seen = in
		reply, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": body}}}})
		_, _ = w.Write(reply)
	}))
	old := answerEndpoint
	answerEndpoint = srv.URL
	t.Cleanup(func() { answerEndpoint = old; srv.Close() })
	return srv, seen
}

func TestSynthesizeAnswerReadsEvidenceAndCites(t *testing.T) {
	_, seen := answerServer(t, `{"status":"ok","answer":"Henri B. Kagan 和 Kenso Soai","cites":[1,3,99]}`)
	cites := []Citation{
		{Title: "2026年诺贝尔化学奖揭晓", Domain: "chinanews.com.cn", Snippet: "授予 Henri B. Kagan 和 Kenso Soai。"},
		{Title: "无关页面", Domain: "example.com", Snippet: "别的新闻。"},
		{Title: "新华网报道", Domain: "xinhuanet.com", Snippet: "两位获奖科学家。"},
	}
	ans := synthesizeAnswer(context.Background(), Keys{DeepSeek: "k"}, "2026年诺贝尔化学奖授予了谁", cites, "deepseek-chat")
	if ans.Status != "ok" {
		t.Fatalf("status = %q error %q", ans.Status, ans.Error)
	}
	if ans.Text != "Henri B. Kagan 和 Kenso Soai" {
		t.Fatalf("answer = %q", ans.Text)
	}
	if len(ans.CiteIndexes) != 2 || ans.CiteIndexes[0] != 0 || ans.CiteIndexes[1] != 2 {
		t.Fatalf("cites = %v, want [0 2] (99 out of range)", ans.CiteIndexes)
	}
	if ans.Model != "deepseek-chat" {
		t.Fatalf("model = %q", ans.Model)
	}
	if ans.LatencyMs <= 0 {
		t.Fatalf("latency = %d, want the measured call time", ans.LatencyMs)
	}
	msgs, _ := (*seen)["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("prompt = %+v", *seen)
	}
	user, _ := msgs[1].(map[string]any)["content"].(string)
	if !strings.Contains(user, "[1]") || !strings.Contains(user, "chinanews.com.cn") {
		t.Fatalf("evidence missing from the prompt: %q", user)
	}
	if !strings.Contains(user, "2026年诺贝尔化学奖授予了谁") {
		t.Fatalf("question missing from the prompt: %q", user)
	}
}

func TestSynthesizeAnswerHonestStates(t *testing.T) {
	cites := []Citation{{Title: "t", Domain: "d", Snippet: "s"}}
	if ans := synthesizeAnswer(context.Background(), Keys{}, "q", cites, ""); ans.Status != "disabled" || ans.Text != "" {
		t.Fatalf("no key: %+v", ans)
	}
	answerServer(t, `{"status":"insufficient","answer":"","cites":[]}`)
	if ans := synthesizeAnswer(context.Background(), Keys{DeepSeek: "k"}, "q", cites, ""); ans.Status != "insufficient" || ans.Text != "" {
		t.Fatalf("insufficient: %+v", ans)
	}
	answerServer(t, `not json at all`)
	if ans := synthesizeAnswer(context.Background(), Keys{DeepSeek: "k"}, "q", cites, ""); ans.Status != "ok" || ans.Text != "not json at all" {
		t.Fatalf("short plain reply: %+v", ans)
	}
}

func TestSynthesisEntersTheReport(t *testing.T) {
	answerServer(t, `{"status":"ok","answer":"授予 Kagan 和 Soai","cites":[1]}`)
	e := newTestEngine(t)
	e.keysFn = func() Keys { return Keys{DeepSeek: "k"} }
	e.channelRunner = func(context.Context, string, Keys, int, map[string]bool) []runResult {
		return []runResult{{
			run: channelRun{Name: "Stub", ChannelID: "stub", RawHits: 1},
			cite: []Citation{{
				Title: "2026年诺贝尔化学奖", URL: "https://news.example.org/a",
				Snippet: "瑞典皇家科学院把 2026 年诺贝尔化学奖授予 Kagan 和 Soai。",
				Channel: "stub", Domain: "news.example.org", SourceName: "新闻", AuthorityLevel: 5,
			}},
		}}
	}
	inv := e.Investigate(context.Background(), Options{Query: "2026年诺贝尔化学奖授予了谁", ModeID: "M11"})
	if inv.Answer.Status != "ok" || !strings.Contains(inv.Answer.Text, "Kagan") {
		t.Fatalf("answer = %+v", inv.Answer)
	}
	if !strings.Contains(inv.ExecutiveSummary, "授予 Kagan") {
		t.Fatalf("summary does not lead with the answer: %q", inv.ExecutiveSummary)
	}
	if !strings.Contains(inv.MarkdownBriefing, "## 答案") || !strings.Contains(inv.MarkdownBriefing, "授予 Kagan") {
		t.Fatalf("briefing has no answer: %q", inv.MarkdownBriefing)
	}
	if !strings.Contains(inv.AgentEnvelopeXML, "<answer") || !strings.Contains(inv.AgentEnvelopeXML, "授予 Kagan") {
		t.Fatalf("envelope has no answer: %q", inv.AgentEnvelopeXML)
	}
	found := false
	for _, st := range inv.Trajectory {
		if strings.Contains(st.Gateway, "答案合成") {
			found = true
		}
	}
	if !found {
		t.Fatal("the synthesis call is not in the trajectory")
	}
}

// The answer is model text like any other: personal data is masked and a
// red-line answer is dropped before it reaches a view.
func TestSynthesisGoesThroughTheOutputGuard(t *testing.T) {
	answerServer(t, `{"status":"ok","answer":"联系电话 `+testPhone+`，邮箱 `+testEmail+`","cites":[1]}`)
	e := newTestEngine(t)
	e.keysFn = func() Keys { return Keys{DeepSeek: "k"} }
	e.channelRunner = func(context.Context, string, Keys, int, map[string]bool) []runResult {
		return []runResult{{
			run:  channelRun{Name: "Stub", ChannelID: "stub", RawHits: 1},
			cite: []Citation{{Title: "冰雹理赔", URL: "https://claims.example.org/a", Snippet: "冰雹理赔进度。", Channel: "stub", Domain: "claims.example.org", AuthorityLevel: 4}},
		}}
	}
	inv := e.Investigate(context.Background(), Options{Query: "北京冰雹理赔进度", ModeID: "M01"})
	if inv.Answer.Status != "ok" {
		t.Fatalf("answer = %+v", inv.Answer)
	}
	if strings.Contains(inv.Answer.Text, testPhone) || strings.Contains(inv.Answer.Text, testEmail) {
		t.Fatalf("PII survived in the answer: %q", inv.Answer.Text)
	}
	if !strings.Contains(inv.ExecutiveSummary, "[PHONE]") && !strings.Contains(inv.ExecutiveSummary, "[EMAIL]") {
		t.Fatalf("the answer in the summary is not masked: %q", inv.ExecutiveSummary)
	}
}
