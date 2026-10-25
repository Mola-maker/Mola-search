// Real connectivity checks: every probe makes the vendor's own call with the
// key the page saved, and reports what came back or why it did not. There is
// no simulated latency and no assumed success.
package providerhub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yetone/magpie/internal/searchdet"
)

// Result is one connectivity check as the page shows it.
type Result struct {
	OK        bool   `json:"ok"`
	Detail    string `json:"detail,omitempty"`
	LatencyMs int64  `json:"latencyMs"`
	CheckedAt string `json:"checkedAt"`
}

var (
	// ErrNotConfigured means the key the provider needs is not set.
	ErrNotConfigured = errors.New("provider is not configured")
	// ErrDisabled means the user turned the provider off.
	ErrDisabled = errors.New("provider is turned off")
)

// probeClient is shared so the platform's checks do not pile up sockets.
var probeClient = &http.Client{Timeout: 10 * time.Second}

// RunProbe checks one provider. A vendor answering with an error is a
// successful check of a failed connection — OK stays false and Detail says
// what the vendor said. Only a provider that cannot be checked at all (no
// key, turned off, unknown id) returns an error.
func RunProbe(ctx context.Context, id string) (Result, error) {
	p, ok := ByID(id)
	if !ok {
		return Result{}, fmt.Errorf("unknown provider %q", id)
	}
	if !Enabled(p) {
		return Result{}, ErrDisabled
	}
	cfg := Config(p)
	if !Configured(p, cfg) {
		return Result{}, ErrNotConfigured
	}
	start := time.Now()
	detail, err := probe(ctx, p, cfg)
	took := time.Since(start)
	res := Result{OK: err == nil, LatencyMs: took.Milliseconds(), CheckedAt: time.Now().UTC().Format(time.RFC3339)}
	if err != nil {
		res.Detail = err.Error()
	} else {
		res.Detail = detail
	}
	return res, nil
}

// baseOf picks the endpoint a probe asks: a test override, the provider's
// slot base field, or its fixed default.
func baseOf(p Provider, cfg map[string]string) string {
	if v := strings.TrimSpace(cfg["__base"]); v != "" {
		return strings.TrimRight(v, "/")
	}
	if p.Slot != "" {
		if v := strings.TrimSpace(cfg[slotBase(p)]); v != "" {
			return strings.TrimRight(v, "/")
		}
	}
	return strings.TrimRight(p.ProbeURL, "/")
}

func probe(ctx context.Context, p Provider, cfg map[string]string) (string, error) {
	switch p.Probe {
	case ProbeChannel:
		n, err := searchdet.ProbeChannel(ctx, p.Channel, p.ProbeURL)
		if errors.Is(err, searchdet.ErrChannelNotConfigured) {
			return "", ErrNotConfigured
		}
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%d citations came back", n), nil

	case ProbeOpenAI, ProbeJina, ProbeCohere:
		body, err := getJSON(ctx, baseOf(p, cfg)+"/models", authHeader("Authorization", "Bearer", keyOf(p, cfg)))
		if err != nil {
			return "", err
		}
		return countList(body, "data", "models"), nil

	case ProbeAnthropic:
		h := map[string]string{"x-api-key": keyOf(p, cfg), "anthropic-version": "2023-06-01"}
		body, err := getJSON(ctx, baseOf(p, cfg)+"/models", h)
		if err != nil {
			return "", err
		}
		return countList(body, "data", "models"), nil

	case ProbeGemini:
		u := baseOf(p, cfg) + "/v1beta/models?key=" + url.QueryEscape(keyOf(p, cfg))
		body, err := getJSON(ctx, u, nil)
		if err != nil {
			return "", err
		}
		return countList(body, "models"), nil

	case ProbeVoyageEmbed:
		payload := map[string]any{"input": []string{"ping"}, "model": modelOf(p, cfg)}
		body, err := postJSON(ctx, baseOf(p, cfg)+"/embeddings", payload, authHeader("Authorization", "Bearer", keyOf(p, cfg)))
		if err != nil {
			return "", err
		}
		var parsed struct {
			Data []struct {
				Embedding []float64 `json:"embedding"`
			} `json:"data"`
		}
		_ = json.Unmarshal(body, &parsed)
		if len(parsed.Data) > 0 && len(parsed.Data[0].Embedding) > 0 {
			return fmt.Sprintf("an embedding of %d dimensions", len(parsed.Data[0].Embedding)), nil
		}
		return "the endpoint answered", nil

	case ProbeVoyageRerank:
		payload := map[string]any{"query": "ping", "documents": []string{"a", "b"}, "model": modelOf(p, cfg)}
		if _, err := postJSON(ctx, baseOf(p, cfg)+"/rerank", payload, authHeader("Authorization", "Bearer", keyOf(p, cfg))); err != nil {
			return "", err
		}
		return "the rerank endpoint answered", nil

	case ProbeFirecrawl:
		u := p.ProbeURL
		if v := strings.TrimSpace(cfg["__base"]); v != "" {
			u = strings.TrimRight(v, "/")
		}
		body, err := getJSON(ctx, u, authHeader("Authorization", "Bearer", keyOf(p, cfg)))
		if err != nil {
			return "", err
		}
		var parsed struct {
			Data struct {
				Remaining int `json:"remaining_credits"`
			} `json:"data"`
		}
		if json.Unmarshal(body, &parsed) == nil && parsed.Data.Remaining > 0 {
			return fmt.Sprintf("%d credits remain", parsed.Data.Remaining), nil
		}
		return "the account answered", nil

	case ProbeDeepgram:
		body, err := getJSON(ctx, baseOf(p, cfg)+"/projects", map[string]string{"Authorization": "Token " + keyOf(p, cfg)})
		if err != nil {
			return "", err
		}
		return countList(body, "projects"), nil

	case ProbeAssembly:
		if _, err := getJSON(ctx, baseOf(p, cfg)+"/account", map[string]string{"authorization": keyOf(p, cfg)}); err != nil {
			return "", err
		}
		return "the account answered", nil

	case ProbeEleven:
		body, err := getJSON(ctx, baseOf(p, cfg)+"/user", map[string]string{"xi-api-key": keyOf(p, cfg)})
		if err != nil {
			return "", err
		}
		var parsed struct {
			Subscription struct {
				Tier string `json:"tier"`
			} `json:"subscription"`
		}
		if json.Unmarshal(body, &parsed) == nil && parsed.Subscription.Tier != "" {
			return "tier " + parsed.Subscription.Tier, nil
		}
		return "the account answered", nil

	case ProbeQdrant:
		h := map[string]string{}
		if k := keyOf(p, cfg); k != "" {
			h["api-key"] = k
		}
		body, err := getJSON(ctx, baseOf(p, cfg)+"/collections", h)
		if err != nil {
			return "", err
		}
		var parsed struct {
			Result struct {
				Collections []struct {
					Name string `json:"name"`
				} `json:"collections"`
			} `json:"result"`
		}
		if json.Unmarshal(body, &parsed) == nil {
			return fmt.Sprintf("%d collections", len(parsed.Result.Collections)), nil
		}
		return "the store answered", nil

	case ProbePinecone:
		body, err := getJSON(ctx, baseOf(p, cfg)+"/indexes", map[string]string{"Api-Key": keyOf(p, cfg)})
		if err != nil {
			return "", err
		}
		var parsed struct {
			Indexes []struct {
				Name string `json:"name"`
			} `json:"indexes"`
		}
		if json.Unmarshal(body, &parsed) == nil {
			return fmt.Sprintf("%d indexes", len(parsed.Indexes)), nil
		}
		return "the store answered", nil

	case ProbeWeaviate:
		h := map[string]string{}
		if k := keyOf(p, cfg); k != "" {
			h["Authorization"] = "Bearer " + k
		}
		if _, err := getJSON(ctx, baseOf(p, cfg)+"/v1/.well-known/ready", h); err != nil {
			return "", err
		}
		return "ready", nil

	case ProbeChroma:
		h := map[string]string{}
		if k := keyOf(p, cfg); k != "" {
			h["Authorization"] = "Bearer " + k
		}
		base := baseOf(p, cfg)
		if _, err := getJSON(ctx, base+"/api/v2/heartbeat", h); err == nil {
			return "heartbeat answered (v2)", nil
		}
		if _, err := getJSON(ctx, base+"/api/v1/heartbeat", h); err == nil {
			return "heartbeat answered (v1)", nil
		}
		return "", fmt.Errorf("%s answered neither v2 nor v1 heartbeat", base)

	case ProbeMilvus:
		if _, err := getJSON(ctx, baseOf(p, cfg)+"/healthz", nil); err != nil {
			return "", err
		}
		return "healthy", nil

	case ProbeOllama:
		base := strings.TrimSuffix(baseOf(p, cfg), "/v1")
		if v := strings.TrimSpace(cfg["OLLAMA_BASE_URL"]); v != "" {
			base = strings.TrimRight(v, "/")
		}
		body, err := getJSON(ctx, base+"/api/tags", nil)
		if err != nil {
			return "", err
		}
		var parsed struct {
			Models []struct {
				Name string `json:"name"`
			} `json:"models"`
		}
		if json.Unmarshal(body, &parsed) == nil {
			return fmt.Sprintf("%d models installed", len(parsed.Models)), nil
		}
		return "the server answered", nil

	case ProbeMCP:
		u := baseOf(p, cfg)
		if v := strings.TrimSpace(cfg["CONTEXT7_MCP_URL"]); v != "" && p.ID == "context7" {
			u = strings.TrimRight(v, "/")
		}
		payload := map[string]any{
			"jsonrpc": "2.0", "id": 1, "method": "initialize",
			"params": map[string]any{
				"protocolVersion": "2025-06-18",
				"capabilities":    map[string]any{},
				"clientInfo":      map[string]any{"name": "molasearch-provider-hub", "version": "1.0"},
			},
		}
		h := map[string]string{"Accept": "application/json, text/event-stream"}
		if k := keyOf(p, cfg); k != "" {
			h["Authorization"] = "Bearer " + k
		}
		body, err := postJSON(ctx, u, payload, h)
		if err != nil {
			return "", err
		}
		if name, version := mcpServerInfo(body); name != "" {
			if version != "" {
				return name + " " + version, nil
			}
			return name, nil
		}
		return "the MCP endpoint answered", nil

	case ProbeLocalPlugin:
		path, version, err := findPlugin(p.Needle)
		if err != nil {
			return "", err
		}
		if version != "" {
			return fmt.Sprintf("found at %s, version %s", path, version), nil
		}
		return "found at " + path, nil

	case ProbePostgres:
		dsn := keyOf(p, cfg)
		u, err := url.Parse(dsn)
		if err != nil || u.Host == "" {
			return "", fmt.Errorf("the DSN has no host to dial")
		}
		host := u.Host
		if u.Port() == "" {
			host = net.JoinHostPort(u.Hostname(), "5432")
		}
		conn, err := net.DialTimeout("tcp", host, 5*time.Second)
		if err != nil {
			return "", err
		}
		conn.Close()
		return "TCP reachable at " + host + " (login not checked)", nil
	}
	return "", fmt.Errorf("no probe for %q", p.Probe)
}

// keyOf is the provider's key field: the one marked secret and required,
// else the first secret field.
func keyOf(p Provider, cfg map[string]string) string {
	for _, f := range p.Fields {
		if f.Secret && f.Required {
			return cfg[f.Env]
		}
	}
	for _, f := range p.Fields {
		if f.Secret {
			return cfg[f.Env]
		}
	}
	return ""
}

// modelOf is the provider's model field.
func modelOf(p Provider, cfg map[string]string) string {
	for _, f := range p.Fields {
		if strings.HasSuffix(f.Env, "_MODEL") {
			return cfg[f.Env]
		}
	}
	return ""
}

func authHeader(name, scheme, key string) map[string]string {
	if key == "" {
		return nil
	}
	return map[string]string{name: scheme + " " + key}
}

func getJSON(ctx context.Context, u string, headers map[string]string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	return doProbe(req, headers)
}

func postJSON(ctx context.Context, u string, payload any, headers map[string]string) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if headers == nil {
		headers = map[string]string{}
	}
	headers["Content-Type"] = "application/json"
	return doProbe(req, headers)
}

// doProbe sends the request and turns every non-2xx into the vendor's own
// words, shortened, so the page can show why a connection failed.
func doProbe(req *http.Request, headers map[string]string) ([]byte, error) {
	for k, v := range headers {
		if v != "" {
			req.Header.Set(k, v)
		}
	}
	resp, err := probeClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("%s answered %d: %s", req.URL.Host, resp.StatusCode, snippet(body))
	}
	return body, nil
}

func snippet(body []byte) string {
	s := strings.Join(strings.Fields(string(body)), " ")
	if len(s) > 180 {
		s = s[:180] + "…"
	}
	if s == "" {
		return "(empty body)"
	}
	return s
}

// countList reports how many entries a common list shape holds.
func countList(body []byte, keys ...string) string {
	var m map[string]json.RawMessage
	if json.Unmarshal(body, &m) != nil {
		return "the endpoint answered"
	}
	for _, k := range keys {
		raw, ok := m[k]
		if !ok {
			continue
		}
		var list []json.RawMessage
		if json.Unmarshal(raw, &list) == nil {
			return fmt.Sprintf("%d entries visible to this key", len(list))
		}
	}
	return "the endpoint answered"
}

// mcpServerInfo reads serverInfo out of a JSON or SSE JSON-RPC reply.
func mcpServerInfo(body []byte) (name, version string) {
	payload := body
	if i := bytes.LastIndex(body, []byte("data:")); i >= 0 {
		payload = bytes.TrimSpace(body[i+len("data:"):])
	}
	var reply struct {
		Result struct {
			ServerInfo struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"serverInfo"`
		} `json:"result"`
	}
	if json.Unmarshal(payload, &reply) != nil {
		return "", ""
	}
	return reply.Result.ServerInfo.Name, reply.Result.ServerInfo.Version
}

// findPlugin looks for a locally installed Claude Code plugin by name and
// returns its folder and version. This is a file check, not a network call:
// these tools live on the computer.
func findPlugin(needle string) (string, string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", err
	}
	root := filepath.Join(home, ".claude", "plugins")
	found := ""
	version := ""
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || found != "" {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if strings.Count(rel, string(filepath.Separator)) > 6 {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if !strings.Contains(strings.ToLower(d.Name()), strings.ToLower(needle)) {
			return nil
		}
		for _, manifest := range []string{"plugin.json", "package.json"} {
			raw, err := os.ReadFile(filepath.Join(path, manifest))
			if err != nil {
				continue
			}
			var m struct {
				Version string `json:"version"`
			}
			if json.Unmarshal(raw, &m) == nil {
				found = rel
				version = m.Version
				return nil
			}
		}
		return nil
	})
	if found == "" {
		return "", "", fmt.Errorf("not installed under ~/.claude/plugins")
	}
	return found, version, nil
}
