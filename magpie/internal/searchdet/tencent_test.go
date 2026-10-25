package searchdet

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestTencentTC3SendsTheOfficialSearchProShape holds the signed request to
// the contract the live endpoint accepted on 2026-10-24: X-TC-Version
// 2025-05-08, X-TC-Action SearchPro, and the body {Query, Mode, Cnt} —
// Count and SearchMode answer UnknownParameter. The signature is recomputed
// from the bytes the server received, so a header or body that drifts from
// what was signed is caught here rather than by a live 401.
func TestTencentTC3SendsTheOfficialSearchProShape(t *testing.T) {
	t.Setenv("TENCENT_WSA_VERSION", "")
	const (
		secretID  = "AKIDexample"
		secretKey = "example-secret"
		query     = "北京今天天气"
	)
	page := `{"title":"北京今天天气","url":"https://weather.example.cn/a","site":"示例气象","date":"2026/10/24 08:00:00","passage":"# 天气预报\n北京晴","content":"全文","authority_level":5}`
	card := `{"title":"","url":"","site":"天气","passage":"# 天气卡","vr":true,"vr_category":"weather"}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/" {
			t.Errorf("%s %s, want POST /", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("X-TC-Action"); got != "SearchPro" {
			t.Errorf("X-TC-Action = %q", got)
		}
		if got := r.Header.Get("X-TC-Version"); got != "2025-05-08" {
			t.Errorf("X-TC-Version = %q, want the version the live endpoint serves", got)
		}

		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Errorf("payload: %v", err)
		}
		for _, key := range []string{"Query", "Mode", "Cnt"} {
			if _, ok := payload[key]; !ok {
				t.Errorf("payload is missing %s: %s", key, body)
			}
		}
		for _, key := range []string{"Count", "SearchMode"} {
			if _, ok := payload[key]; ok {
				t.Errorf("payload still sends %s: %s", key, body)
			}
		}
		if payload["Query"] != query || payload["Mode"] != float64(2) || payload["Cnt"] != float64(10) {
			t.Errorf("payload = %s, want Query/Mode 2/Cnt 10 for n=8", body)
		}

		// Recompute the TC3 signature independently of the channel's helpers.
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "TC3-HMAC-SHA256 Credential="+secretID+"/") {
			t.Errorf("Authorization = %q", auth)
			return
		}
		scope := "Credential=" + secretID + "/"
		rest := strings.TrimPrefix(auth, "TC3-HMAC-SHA256 "+scope)
		parts := strings.SplitN(rest, ", ", 2)
		scope = parts[0] // date/wsa/tc3_request
		date := strings.SplitN(scope, "/", 2)[0]
		signature := ""
		if i := strings.Index(rest, "Signature="); i >= 0 {
			signature = rest[i+len("Signature="):]
		}
		ts, _ := strconv.ParseInt(r.Header.Get("X-TC-Timestamp"), 10, 64)
		if want := time.Unix(ts, 0).UTC().Format("2006-01-02"); want != date {
			t.Errorf("credential date = %q, want %q (UTC)", date, want)
		}
		canonical := strings.Join([]string{
			"POST", "/", "",
			"content-type:application/json; charset=utf-8\nhost:wsa.tencentcloudapi.com\nx-tc-action:searchpro\n",
			"content-type;host;x-tc-action",
			hexSHA256(body),
		}, "\n")
		toSign := strings.Join([]string{
			"TC3-HMAC-SHA256",
			r.Header.Get("X-TC-Timestamp"),
			scope,
			hexSHA256([]byte(canonical)),
		}, "\n")
		key := hmacSHA256([]byte("TC3"+secretKey), date)
		key = hmacSHA256(key, "wsa")
		key = hmacSHA256(key, "tc3_request")
		if want := hex.EncodeToString(hmacSHA256(key, toSign)); want != signature {
			t.Errorf("signature = %q, want %q", signature, want)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"Response": map[string]any{
			"RequestId": "test", "Query": query, "Version": "standard",
			"Pages": []string{page, card},
		}})
	}))
	defer srv.Close()

	old := tencentWSAURL
	tencentWSAURL = srv.URL + "/"
	defer func() { tencentWSAURL = old }()

	cites, err := tencentTC3(context.Background(), query, Keys{TencentSecretID: secretID, TencentSecretKey: secretKey}, 8)
	if err != nil {
		t.Fatal(err)
	}
	// The vertical card has no page of its own: url is empty, so it is not
	// a citation (its answer is in the passage, not a source a reader visits).
	if len(cites) != 1 {
		t.Fatalf("citations = %d, want 1: %+v", len(cites), cites)
	}
	c := cites[0]
	if c.Title != "北京今天天气" || c.URL != "https://weather.example.cn/a" {
		t.Fatalf("citation = %+v", c)
	}
	if c.Snippet != "# 天气预报 北京晴" {
		t.Fatalf("snippet = %q, want the passage with whitespace collapsed", c.Snippet)
	}
	if c.PublishedAt != "2026-10-24" {
		t.Fatalf("published = %q, want the engine's date shape", c.PublishedAt)
	}
	if c.SourceName != "示例气象" || c.AuthorityLevel != 5 {
		t.Fatalf("source = %q authority = %d", c.SourceName, c.AuthorityLevel)
	}
	if c.Channel != "tencent" {
		t.Fatalf("channel = %q", c.Channel)
	}
}

// TestParseTencentReplyReadsTheSavedSearchProReply parses a real 200 reply
// saved from the live endpoint: every page arrives as a JSON-encoded string
// inside Response.Pages, not as an object.
func TestParseTencentReplyReadsTheSavedSearchProReply(t *testing.T) {
	raw, err := os.ReadFile("testdata/tencent_searchpro.json")
	if err != nil {
		t.Fatal(err)
	}
	cites, err := parseTencentReply(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(cites) != 10 {
		t.Fatalf("citations = %d, want the 10 pages the reply carried", len(cites))
	}
	first := cites[0]
	if first.URL != "https://www.cac.gov.cn/2026-08/10/c_1788108060237101.htm" {
		t.Fatalf("first url = %q", first.URL)
	}
	if !strings.Contains(first.Title, "人工智能") {
		t.Fatalf("first title = %q", first.Title)
	}
	if first.PublishedAt != "2026-08-10" {
		t.Fatalf("first published = %q", first.PublishedAt)
	}
	for i, c := range cites {
		if c.URL == "" || c.Title == "" || c.Snippet == "" {
			t.Fatalf("citation %d is empty: %+v", i, c)
		}
		if strings.ContainsAny(c.Snippet, "\r\n") {
			t.Fatalf("citation %d kept a line break: %q", i, c.Snippet)
		}
		if n := len([]rune(c.Snippet)); n > 300 {
			t.Fatalf("citation %d snippet is %d runes", i, n)
		}
		if c.Channel != "tencent" {
			t.Fatalf("citation %d channel = %q", i, c.Channel)
		}
	}
}

// TestParseTencentReplyTakesTheMCPTextShape: the MCP tool's text is the same
// document, and a bare array of pages reads too.
func TestParseTencentReplyTakesTheMCPTextShape(t *testing.T) {
	page := `{"title":"标题","url":"https://example.cn/a","site":"示例","date":"2026/10/01","passage":"摘要"}`
	for _, body := range []string{
		`{"Response":{"Pages":[` + jsonQuote(page) + `]}}`,
		`[` + jsonQuote(page) + `]`,
		`[` + page + `]`,
	} {
		cites, err := parseTencentReply([]byte(body))
		if err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		if len(cites) != 1 || cites[0].URL != "https://example.cn/a" {
			t.Fatalf("%s -> %+v", body, cites)
		}
	}
}

// TestParseTencentReplySurfacesVendorErrors uses the two replies the live
// endpoint sent when the request was wrong: they are errors to report, not
// an empty result.
func TestParseTencentReplySurfacesVendorErrors(t *testing.T) {
	cases := []struct {
		body string
		want string
	}{
		{
			`{"Response":{"Error":{"Code":"InvalidAction","Message":"The request action=` + "`SearchPro`" + ` is invalid or not found in service=` + "`wsa`" + ` and version=` + "`2025-05-22`" + `."},"RequestId":"x"}}`,
			"InvalidAction",
		},
		{
			`{"Response":{"Error":{"Code":"UnknownParameter","Message":"The parameter ` + "`Count`" + ` is not recognized."},"RequestId":"x"}}`,
			"UnknownParameter",
		},
	}
	for _, c := range cases {
		_, err := parseTencentReply([]byte(c.body))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("err = %v, want it to name %s", err, c.want)
		}
	}
	if _, err := parseTencentReply([]byte(`{"Response":{"Pages":[]}}`)); err != nil {
		t.Fatalf("an empty page list is not an error: %v", err)
	}
	if _, err := parseTencentReply([]byte(`<html>gateway</html>`)); err == nil {
		t.Fatal("an HTML body was read as a reply")
	}
}

// TestTencentMCPCallsSearchProWithTheVendorSchema drives the MCP wire
// offline: initialize, the notification the server acknowledges with 202,
// then tools/call. Cnt is a string here — the vendor's own tool schema says
// so — and the reply arrives as an SSE stream.
func TestTencentMCPCallsSearchProWithTheVendorSchema(t *testing.T) {
	page := `{"title":"北京天气","url":"https://weather.example.cn/b","site":"示例天气","date":"2026/10/24","passage":"晴"}`
	envelope := `{"Response":{"Pages":[` + jsonQuote(page) + `]}}`

	var (
		mu          sync.Mutex
		calls       int
		initialized bool
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var msg map[string]any
		if err := json.Unmarshal(body, &msg); err != nil {
			t.Errorf("rpc body: %v", err)
			return
		}
		switch msg["method"] {
		case "initialize":
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18","serverInfo":{"name":"wsa"}}}`)
		case "notifications/initialized":
			mu.Lock()
			initialized = true
			mu.Unlock()
			w.WriteHeader(http.StatusAccepted)
		case "tools/call":
			mu.Lock()
			calls++
			mu.Unlock()
			if got := r.Header.Get("Authorization"); got != "Bearer wsa-key" {
				t.Errorf("Authorization = %q, want the WSA API key, not the TC3 pair", got)
			}
			if got := r.Header.Get("Accept"); !strings.Contains(got, "text/event-stream") {
				t.Errorf("Accept = %q", got)
			}
			params, _ := msg["params"].(map[string]any)
			if params["name"] != "SearchPro" {
				t.Errorf("tool = %v", params["name"])
			}
			args, _ := params["arguments"].(map[string]any)
			if args["Cnt"] != "10" {
				t.Errorf("Cnt = %#v, want the string \"10\"", args["Cnt"])
			}
			if args["Mode"] != float64(2) || args["Query"] != "北京天气" {
				t.Errorf("arguments = %v", args)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "event: message\ndata: %s\n\n", `{"jsonrpc":"2.0","id":2,"result":{"content":[{"type":"text","text":`+jsonQuote(envelope)+`}]}}`)
		default:
			t.Errorf("unexpected method %v", msg["method"])
		}
	}))
	defer srv.Close()

	old := tencentMCPURL
	tencentMCPURL = srv.URL
	defer func() { tencentMCPURL = old }()

	cites, err := tencentMCP(context.Background(), "北京天气", Keys{TencentWSAKey: "wsa-key"}, 8)
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	gotInitialized, gotCalls := initialized, calls
	mu.Unlock()
	if !gotInitialized || gotCalls != 1 {
		t.Fatalf("initialized=%v calls=%d", gotInitialized, gotCalls)
	}
	if len(cites) != 1 || cites[0].URL != "https://weather.example.cn/b" || cites[0].PublishedAt != "2026-10-24" {
		t.Fatalf("citations = %+v", cites)
	}
}

// TestMCPPayloadUnwrapsSSE: the endpoint may answer a single JSON body or
// an SSE stream whose last data: line holds the JSON-RPC message.
func TestMCPPayloadUnwrapsSSE(t *testing.T) {
	plain := []byte(`{"jsonrpc":"2.0","id":1,"result":{}}`)
	if got := string(mcpPayload(plain)); got != string(plain) {
		t.Fatalf("plain body = %q", got)
	}
	sse := []byte("event: message\r\ndata: {\"jsonrpc\":\"2.0\",\"id\":1}\r\n\r\n")
	if got := string(mcpPayload(sse)); got != `{"jsonrpc":"2.0","id":1}` {
		t.Fatalf("sse body = %q", got)
	}
}

// TestTencentCntSnapsToAllowedValues: the vendor accepts 10/20/30/40/50 only.
func TestTencentCntSnapsToAllowedValues(t *testing.T) {
	for n, want := range map[int]int{1: 10, 8: 10, 10: 10, 11: 20, 25: 30, 40: 40, 50: 50, 80: 50} {
		if got := tencentCnt(n); got != want {
			t.Errorf("tencentCnt(%d) = %d, want %d", n, got, want)
		}
	}
}

// TestTencentChannelGatesOnItsOwnKeys: the TC3 pair and the MCP key are
// different credentials, and either one turns the channel on.
func TestTencentChannelGatesOnItsOwnKeys(t *testing.T) {
	tc3 := Keys{TencentSecretID: "id", TencentSecretKey: "key"}
	if tc3.TencentMCPReady() {
		t.Fatal("the TC3 pair was accepted as an MCP key")
	}
	if !(tencentChannel{}).Enabled(tc3) {
		t.Fatal("the channel is off with a TC3 pair")
	}
	mcp := Keys{TencentWSAKey: "wsa-key"}
	if !mcp.TencentMCPReady() || !(tencentChannel{}).Enabled(mcp) {
		t.Fatal("the channel is off with an MCP key")
	}
	if (tencentChannel{}).Enabled(Keys{}) {
		t.Fatal("the channel is on with no key")
	}
}

// hexSHA256 is the test's own digest, so the signature check does not lean
// on the channel's helper.
func hexSHA256(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func hmacSHA256(key []byte, msg string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(msg))
	return m.Sum(nil)
}

// jsonQuote quotes s as a JSON string.
func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
