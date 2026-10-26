// Answer synthesis: the evidence envelope is what the channels returned;
// this step reads that evidence and states the answer in one short line,
// with the citation numbers it used. It is a real model call over real
// citations, not a template: no key or a failed call leaves the answer
// empty and says why, so the page never shows an invented answer.
package searchdet

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// answerEndpoint is the model's chat-completions address. A test swaps it
// for an httptest server, so no test reaches the network.
var answerEndpoint = "https://api.deepseek.com/chat/completions"

// Answer is the synthesized line and where it came from.
type Answer struct {
	Text        string `json:"text,omitempty"`
	CiteIndexes []int  `json:"citeIndexes,omitempty"`
	Model       string `json:"model,omitempty"`
	Status      string `json:"status"` // ok | insufficient | disabled | error
	Error       string `json:"error,omitempty"`
	LatencyMs   int    `json:"latencyMs"`
}

// answerPrompt is strict on purpose: only the numbered evidence, a short
// answer, and the citation numbers that support it. INSUFFICIENT is a real
// answer for the model to give; the caller keeps the run's own verdict.
const answerSystemPrompt = `你是检索取证助手。只使用下面编号的证据作答，不得使用证据之外的知识。
规则：
1) 答案要短：人名、数字、日期或一句话，不要复述问题。
2) 证据不足以回答时，status 必须是 "insufficient"，answer 留空。
3) cites 只填真正支持答案的证据编号（从 1 开始），不要编造编号。
4) 只输出一个 JSON 对象，不要代码块：{"status":"ok|insufficient","answer":"...","cites":[1,2]}`

// synthesizeAnswer asks the model to state the answer held by the evidence.
func synthesizeAnswer(ctx context.Context, k Keys, query string, cites []Citation, model string) (ans Answer) {
	ans = Answer{Model: model, Status: "error"}
	started := time.Now()
	// Named return, so the measured latency is what the caller sees: a
	// defer over a value-returned copy would leave it at 0.
	defer func() { ans.LatencyMs = int(time.Since(started).Milliseconds()) }()
	if strings.TrimSpace(k.DeepSeek) == "" {
		ans.Status, ans.Error = "disabled", "no DEEPSEEK_API_KEY"
		return ans
	}
	evidence := answerEvidenceOf(cites, 10)
	if evidence == "" {
		ans.Status, ans.Error = "insufficient", "no evidence to read"
		return ans
	}
	if strings.TrimSpace(model) == "" {
		model = "deepseek-chat"
		ans.Model = model
	}
	payload := map[string]any{
		"model": model,
		"messages": []map[string]string{
			{"role": "system", "content": answerSystemPrompt},
			{"role": "user", "content": "问题：" + query + "\n\n证据：\n" + evidence},
		},
		"temperature": 0,
		"max_tokens":  300,
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	cctx, cancel := channelDeadline(ctx, 30*time.Second)
	defer cancel()
	if err := postJSON(cctx, answerEndpoint, map[string]string{
		"Authorization": "Bearer " + k.DeepSeek,
	}, payload, &out); err != nil {
		ans.Error = "answer synthesis failed: " + err.Error()
		return ans
	}
	if len(out.Choices) == 0 {
		ans.Error = "answer synthesis failed: empty reply"
		return ans
	}
	raw := strings.TrimSpace(out.Choices[0].Message.Content)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)
	var parsed struct {
		Status string `json:"status"`
		Answer string `json:"answer"`
		Cites  []int  `json:"cites"`
	}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		// A model that ignored the JSON shape but said something short is
		// still a real answer: keep the text, with no citation claims.
		if len([]rune(raw)) <= 200 && raw != "" && !strings.Contains(raw, "\n") {
			ans.Status, ans.Text = "ok", raw
			return ans
		}
		ans.Error = "answer synthesis failed: reply is not JSON"
		return ans
	}
	if strings.EqualFold(parsed.Status, "insufficient") || strings.TrimSpace(parsed.Answer) == "" {
		ans.Status = "insufficient"
		return ans
	}
	ans.Status = "ok"
	ans.Text = strings.TrimSpace(parsed.Answer)
	for _, n := range parsed.Cites {
		if n >= 1 && n <= len(cites) {
			ans.CiteIndexes = appendUniqueInt(ans.CiteIndexes, n-1)
		}
	}
	return ans
}

// answerEvidenceOf numbers the strongest citations for the prompt: title,
// domain and a bounded snippet, so one huge page cannot crowd the rest out.
func answerEvidenceOf(cites []Citation, max int) string {
	var b strings.Builder
	for i, c := range cites {
		if i >= max {
			break
		}
		fmt.Fprintf(&b, "[%d] %s — %s\n%s\n\n", i+1, c.Title, c.Domain, firstRunesOf(c.Snippet, 320))
	}
	return strings.TrimSpace(b.String())
}

func appendUniqueInt(xs []int, v int) []int {
	for _, x := range xs {
		if x == v {
			return xs
		}
	}
	return append(xs, v)
}
