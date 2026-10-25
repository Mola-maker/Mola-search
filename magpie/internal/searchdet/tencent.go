// Tencent Cloud WSA SearchPro: one keyed provider among equals, asked when
// the user gave it a SecretId and SecretKey. Two wires: the signed
// TC3-HMAC-SHA256 call to wsa.tencentcloudapi.com, and the vendor's official
// MCP endpoint. The MCP endpoint authenticates with its own WSA API key, not
// the SecretId/SecretKey pair, so it runs only when that key is set. Without
// either key the channel is off and the keyless aggregators carry the run.
package searchdet

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	tencentWSAHost    = "wsa.tencentcloudapi.com"
	tencentWSAService = "wsa"
	// tencentWSAVersionDefault is the version that serves SearchPro. It is
	// not a guess: on 2026-10-24 the live endpoint answered 2025-05-22 with
	// InvalidAction ("action=SearchPro is invalid ... version=2025-05-22")
	// and served 2025-05-08. TENCENT_WSA_VERSION overrides it for the day
	// the vendor moves; a stale version still answers a readable error.
	tencentWSAVersionDefault = "2025-05-08"
)

// The vendor's official MCP endpoint — JSON-RPC 2.0 over Streamable HTTP. Its
// Bearer is a WSA API key; a Bearer of SecretId:SecretKey answers
// UnauthorizedOperation ("未授权操作。"). A variable, like the signed
// endpoint, so a test can stand a local server in its place.
var (
	tencentWSAURL = "https://" + tencentWSAHost + "/"
	tencentMCPURL = "https://api.wsa.cloud.tencent.com/Mcp"
)

func tencentWSAVersion() string {
	if v := os.Getenv("TENCENT_WSA_VERSION"); v != "" {
		return v
	}
	return tencentWSAVersionDefault
}

// tencentChannel asks Tencent Cloud's WSA SearchPro over the signed TC3 API,
// falling back to the MCP endpoint when that key is configured.
type tencentChannel struct{}

func (tencentChannel) ID() string   { return "tencent" }
func (tencentChannel) Name() string { return "腾讯云 WSA SearchPro" }
func (tencentChannel) Enabled(k Keys) bool {
	return k.TencentReady() || k.TencentMCPReady()
}

func (tencentChannel) Search(ctx context.Context, query string, k Keys, n int) ([]Citation, error) {
	var errs []error
	if k.TencentReady() {
		cites, err := tencentTC3(ctx, query, k, n)
		if len(cites) > 0 {
			return cites, nil
		}
		if err != nil {
			errs = append(errs, err)
		}
	}
	if k.TencentMCPReady() {
		cites, err := tencentMCP(ctx, query, k, n)
		if len(cites) > 0 {
			return cites, nil
		}
		if err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return nil, errNoResults
}

// tencentCnt is the vendor's allowed result count — 10, 20, 30, 40 or 50 —
// as the smallest one that still covers n.
func tencentCnt(n int) int {
	for _, c := range []int{10, 20, 30, 40, 50} {
		if n <= c {
			return c
		}
	}
	return 50
}

// tencentTC3 signs and calls SearchPro per Tencent Cloud's TC3 scheme.
//
// The vendor's own schema names the parameters (its MCP tools/list describes
// the same tool): Query, Mode (0 public web, 1 authoritative vertical, 2
// mixed), Site, Freshness (dN/mN/yN), Cnt (10/20/30/40/50) and Industry
// (gov/news/acad/finance). Mode 2 is the investigation engine's default: a
// run needs public pages and authoritative vertical answers alike.
//
// Cnt and Mode are numbers on this wire; the MCP tool declares Cnt a string.
// Each wire sends its own shape — the signed API answered UnknownParameter
// for Count and SearchMode, names the vendor does not use.
func tencentTC3(ctx context.Context, query string, k Keys, n int) ([]Citation, error) {
	payload, _ := json.Marshal(map[string]any{
		"Query": query,
		"Mode":  2,
		"Cnt":   tencentCnt(n),
	})
	now := time.Now()
	timestamp := now.Unix()
	date := now.UTC().Format("2006-01-02")

	hashedPayload := sha256Hex(payload)
	signedHeaders := "content-type;host;x-tc-action"
	canonicalRequest := strings.Join([]string{
		"POST",
		"/",
		"",
		"content-type:application/json; charset=utf-8\nhost:" + tencentWSAHost + "\nx-tc-action:" + strings.ToLower("SearchPro") + "\n",
		signedHeaders,
		hashedPayload,
	}, "\n")
	credentialScope := date + "/" + tencentWSAService + "/tc3_request"
	stringToSign := strings.Join([]string{
		"TC3-HMAC-SHA256",
		itoa(int(timestamp)),
		credentialScope,
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")

	signingKey := tc3HMAC(tc3HMAC(tc3HMAC(tc3HMAC([]byte("TC3"+k.TencentSecretKey), date), tencentWSAService), "tc3_request"), stringToSign)
	signature := hex.EncodeToString(signingKey)

	authorization := "TC3-HMAC-SHA256 Credential=" + k.TencentSecretID + "/" + credentialScope +
		", SignedHeaders=" + signedHeaders + ", Signature=" + signature

	body, err := postRaw(ctx, tencentWSAURL, map[string]string{
		"Authorization":  authorization,
		"Content-Type":   "application/json; charset=utf-8",
		"X-TC-Action":    "SearchPro",
		"X-TC-Timestamp": itoa(int(timestamp)),
		"X-TC-Version":   tencentWSAVersion(),
	}, string(payload))
	if err != nil {
		return nil, err
	}
	return parseTencentReply(body)
}

// tencentMCP asks the official MCP endpoint over JSON-RPC (initialize, then
// tools/call SearchPro).
func tencentMCP(ctx context.Context, query string, k Keys, n int) ([]Citation, error) {
	header := map[string]string{
		"Authorization": "Bearer " + k.TencentWSAKey,
		"Accept":        "application/json, text/event-stream",
	}
	var init struct {
		Result struct {
			ProtocolVersion string          `json:"protocolVersion"`
			ServerInfo      json.RawMessage `json:"serverInfo"`
		} `json:"result"`
	}
	if err := postMCP(ctx, tencentMCPURL, header, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "molasearch", "version": "5.0"},
		},
	}, &init); err != nil {
		return nil, err
	}
	_ = postMCP(ctx, tencentMCPURL, header, map[string]any{
		"jsonrpc": "2.0", "method": "notifications/initialized",
	}, nil)
	var call struct {
		Result struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := postMCP(ctx, tencentMCPURL, header, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "SearchPro", "arguments": map[string]any{
			"Query": query,
			"Mode":  2,
			"Cnt":   itoa(tencentCnt(n)),
		}},
	}, &call); err != nil {
		return nil, err
	}
	var cites []Citation
	var errs []error
	for _, c := range call.Result.Content {
		if c.Type != "text" {
			continue
		}
		got, err := parseTencentReply([]byte(c.Text))
		if err != nil {
			errs = append(errs, err)
			continue
		}
		cites = append(cites, got...)
	}
	if len(cites) == 0 && len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return cites, nil
}

// postMCP posts one JSON-RPC message and reads the reply, which the
// endpoint may send as a single JSON body or as an SSE stream.
func postMCP(ctx context.Context, url string, header map[string]string, in, out any) error {
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	raw, err := postRawAccepted(ctx, url, header, string(body))
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	payload := mcpPayload(raw)
	var probe struct {
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(payload, &probe); err != nil {
		return fmt.Errorf("mcp: unreadable reply: %w", err)
	}
	if probe.Error != nil {
		return fmt.Errorf("mcp error %d: %s", probe.Error.Code, probe.Error.Message)
	}
	return json.Unmarshal(payload, out)
}

// mcpPayload unwraps a Streamable HTTP reply: a plain JSON body is returned
// as it is, and an SSE stream yields its last data: line, which carries the
// JSON-RPC message.
func mcpPayload(raw []byte) []byte {
	trimmed := bytes.TrimSpace(raw)
	if bytes.HasPrefix(trimmed, []byte("{")) || bytes.HasPrefix(trimmed, []byte("[")) {
		return trimmed
	}
	var last []byte
	for _, line := range bytes.Split(trimmed, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if bytes.HasPrefix(line, []byte("data:")) {
			if data := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:"))); len(data) > 0 {
				last = data
			}
		}
	}
	if len(last) > 0 {
		return last
	}
	return trimmed
}

// tencentDoc is one element of Response.Pages. The API sends each page as a
// JSON-encoded string, not as an object, so the parser unmarshals twice. The
// field list is the vendor's: authority_level, deeplinks, pics and the
// vr/vr_category pair are optional and absent from replies that lack them.
type tencentDoc struct {
	Title          string  `json:"title"`
	URL            string  `json:"url"`
	Site           string  `json:"site"`
	Date           string  `json:"date"`
	Passage        string  `json:"passage"`
	Content        string  `json:"content"`
	Score          float64 `json:"score"`
	AuthorityLevel int     `json:"authority_level"`
	VR             bool    `json:"vr"`
	VRCategory     string  `json:"vr_category"`
}

// citation turns one page into a citation, or reports that there is no page
// to cite: a vertical card (weather, gold price) carries its answer in the
// passage and leaves url empty.
func (d tencentDoc) citation() (Citation, bool) {
	u := strings.TrimSpace(d.URL)
	if u == "" {
		return Citation{}, false
	}
	text := d.Passage
	if strings.TrimSpace(text) == "" {
		text = d.Content
	}
	snippet := firstRunesOf(strings.Join(strings.Fields(text), " "), 300)
	c := cite(strings.TrimSpace(d.Title), u, snippet, "tencent", tencentDate(d.Date))
	c.SourceName = strings.TrimSpace(d.Site)
	if d.AuthorityLevel >= 1 && d.AuthorityLevel <= 5 {
		c.AuthorityLevel = d.AuthorityLevel
	}
	return c, true
}

// tencentDate turns the API's "2026/08/10 11:04:00" into the engine's date
// shape; anything else passes through so a vendor change is still read.
func tencentDate(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 10 && s[4] == '/' && s[7] == '/' {
		return strings.ReplaceAll(s[:10], "/", "-")
	}
	return s
}

// parseTencentReply reads what either wire answered: the TC3 envelope
// {"Response":{"Pages":["{...}"]}}, the same document as the MCP tool's
// text, or a bare array of pages. A vendor error is an error, not an empty
// result — the caller has to see InvalidAction or UnknownParameter.
func parseTencentReply(body []byte) ([]Citation, error) {
	var probe any
	if err := json.Unmarshal(body, &probe); err != nil {
		return nil, fmt.Errorf("tencent: unreadable reply: %w", err)
	}
	switch v := probe.(type) {
	case map[string]any:
		env, _ := v["Response"].(map[string]any)
		if env == nil {
			return nil, errors.New("tencent: reply has no Response object")
		}
		if e, ok := env["Error"].(map[string]any); ok {
			code, _ := e["Code"].(string)
			msg, _ := e["Message"].(string)
			return nil, errors.New("tencent " + code + ": " + msg)
		}
		pages, _ := env["Pages"].([]any)
		return tencentCites(pages), nil
	case []any:
		return tencentCites(v), nil
	}
	return nil, fmt.Errorf("tencent: unexpected reply shape %T", probe)
}

// tencentCites turns Response.Pages into citations. A page arrives as a
// JSON-encoded string; an object is accepted too, so a wire that inlines the
// page still reads.
func tencentCites(pages []any) []Citation {
	var out []Citation
	for _, p := range pages {
		var doc tencentDoc
		switch v := p.(type) {
		case string:
			if json.Unmarshal([]byte(v), &doc) != nil {
				continue
			}
		case map[string]any:
			raw, err := json.Marshal(v)
			if err != nil || json.Unmarshal(raw, &doc) != nil {
				continue
			}
		default:
			continue
		}
		if c, ok := doc.citation(); ok {
			out = append(out, c)
		}
	}
	return out
}

// tc3HMAC is HMAC-SHA256(key, msg), the TC3 chain's step.
func tc3HMAC(key []byte, msg string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(msg))
	return m.Sum(nil)
}

// sha256Hex is hex(SHA-256(bytes)).
func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// postRaw posts a raw string body and reads the reply as bytes.
func postRaw(ctx context.Context, u string, header map[string]string, body string) ([]byte, error) {
	return postRawWith(ctx, u, header, body, doBytes)
}

// postRawAccepted is postRaw for the MCP transport, which acknowledges a
// notification with 202 Accepted as well as answering 200.
func postRawAccepted(ctx context.Context, u string, header map[string]string, body string) ([]byte, error) {
	return postRawWith(ctx, u, header, body, doBytesAccepted)
}

func postRawWith(ctx context.Context, u string, header map[string]string, body string, read func(*http.Request) ([]byte, error)) ([]byte, error) {
	req, err := httpNewRequest(ctx, http.MethodPost, u, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", ua)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	return read(req)
}
