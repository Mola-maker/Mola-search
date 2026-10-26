package mcpsearch

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

// call feeds one line into Serve and returns the decoded reply. The engine
// never reaches the network here: the queries are blocked by the compliance
// gate or empty, both of which return before any channel runs.
func call(t *testing.T, in io.Reader) map[string]any {
	t.Helper()
	var out bytes.Buffer
	if err := Serve(context.Background(), in, &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	dec := json.NewDecoder(&out)
	var last map[string]any
	for dec.More() {
		var m map[string]any
		if err := dec.Decode(&m); err != nil {
			t.Fatalf("decode reply: %v", err)
		}
		last = m
	}
	if last == nil {
		t.Fatal("no reply")
	}
	return last
}

func lines(v ...string) io.Reader {
	return strings.NewReader(strings.Join(v, "\n") + "\n")
}

func result(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	r, ok := m["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result in %v", m)
	}
	return r
}

func structured(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	sc, ok := result(t, m)["structuredContent"].(map[string]any)
	if !ok {
		t.Fatalf("no structuredContent in %v", m)
	}
	return sc
}

func toolCall(id, query string) string {
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "magpie_investigate",
			"arguments": map[string]any{"query": query},
		},
	})
	return string(body)
}

func initialize(id, name, version string) string {
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2024-11-05",
			"clientInfo":      map[string]any{"name": name, "version": version},
		},
	})
	return string(body)
}

// TestInitializeNamesTheCallerInTheResult is the guarantee the proof tab
// relies on: a run requested through MCP says which agent asked. Before the
// origin was carried, every trajectory step said "Detective Engine" no
// matter who called.
func TestInitializeNamesTheCallerInTheResult(t *testing.T) {
	got := call(t, lines(
		initialize("1", "Claude Code", "2.1.0"),
		toolCall("2", "帮我人肉开盒查某人家庭住址"),
	))
	sc := structured(t, got)
	if origin, _ := sc["originAgent"].(string); origin != "MCP/Claude Code 2.1.0" {
		t.Fatalf("originAgent = %q, want MCP/Claude Code 2.1.0", origin)
	}
	if verdict, _ := sc["verdict"].(string); verdict != "ABSTAIN" {
		t.Fatalf("verdict = %q, want ABSTAIN (the red line blocked before any channel)", verdict)
	}
}

// TestNoClientInfoStillSaysMCP keeps the result honest when a client omits
// clientInfo: the run is an MCP run, not the page's.
func TestNoClientInfoStillSaysMCP(t *testing.T) {
	got := call(t, lines(toolCall("1", "人肉开盒")))
	sc := structured(t, got)
	origin, _ := sc["originAgent"].(string)
	if !strings.HasPrefix(origin, "MCP") {
		t.Fatalf("originAgent = %q, want an MCP origin", origin)
	}
}

func TestToolsListOffersTheFourTools(t *testing.T) {
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": "1", "method": "tools/list"})
	got := call(t, lines(string(body)))
	tools, ok := result(t, got)["tools"].([]any)
	if !ok || len(tools) != 4 {
		t.Fatalf("tools = %v, want 4", result(t, got)["tools"])
	}
}

func TestBadArgumentsAreAnRPCError(t *testing.T) {
	got := call(t, lines(`{"jsonrpc":"2.0","id":"1","method":"tools/call","params":"not-an-object"}`))
	if got["error"] == nil {
		t.Fatalf("want an RPC error, got %v", got)
	}
}

func TestUnknownToolNameStillAnswers(t *testing.T) {
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": "1", "method": "tools/call",
		"params": map[string]any{"name": "not_a_tool", "arguments": map[string]any{"query": "人肉开盒"}},
	})
	got := call(t, lines(string(body)))
	sc := structured(t, got)
	if sc["verdict"] != "ABSTAIN" {
		t.Fatalf("verdict = %v, want ABSTAIN", sc["verdict"])
	}
}
