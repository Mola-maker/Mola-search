// Key resolution for MolaSearch: process environment first, then a .env
// file beside the data dir or the executable, then the search APIs the
// user already configured in magpie (providers.json). No key anywhere
// means the keyless channels carry the run — every keyed channel is
// optional.
package searchdet

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/yetone/magpie/internal/appdir"
	"github.com/yetone/magpie/internal/provider"
)

// DataDir is where MolaSearch keeps its state: MOLASEARCH_DATA if set, else
// a data/ directory beside the executable when it exists (so the demo tree
// stays portable), else a mola-search folder under magpie's appdir.
func DataDir() string {
	if d := os.Getenv("MOLASEARCH_DATA"); d != "" {
		_ = os.MkdirAll(d, 0o755)
		return d
	}
	if exe, err := os.Executable(); err == nil {
		d := filepath.Join(filepath.Dir(exe), "data")
		if st, err := os.Stat(d); err == nil && st.IsDir() {
			return d
		}
	}
	d := filepath.Join(appdir.Config(), "mola-search")
	_ = os.MkdirAll(d, 0o755)
	return d
}

var (
	envMu     sync.Mutex
	envLoaded bool
	dotEnv    map[string]string
	dotEnvAt  string
)

// ReloadEnv re-reads the .env file after the provider platform writes it, so
// a key saved there is used by the next request instead of at next start.
func ReloadEnv() {
	envMu.Lock()
	envLoaded = false
	dotEnv = nil
	dotEnvAt = ""
	envMu.Unlock()
}

// loadDotEnv reads the first .env that exists: MOLASEARCH_ENV if set, else
// <data dir>/.env, else .env beside the executable, else ./.env.
// The caller holds envMu.
func loadDotEnvLocked() {
	dotEnv = map[string]string{}
	dotEnvAt = ""
	for _, p := range envFilePaths() {
		if f, err := os.Open(p); err == nil {
			dotEnvAt = p
			sc := bufio.NewScanner(f)
			for sc.Scan() {
				line := strings.TrimSpace(sc.Text())
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				k, v, ok := strings.Cut(line, "=")
				if !ok {
					continue
				}
				k = strings.TrimSpace(k)
				v = strings.TrimSpace(v)
				v = strings.Trim(v, `"'`)
				if k != "" {
					dotEnv[k] = v
				}
			}
			f.Close()
			return
		}
	}
}

// envValue reads one name from the loaded .env, loading it first.
func envValue(name string) string {
	envMu.Lock()
	defer envMu.Unlock()
	if !envLoaded {
		loadDotEnvLocked()
		envLoaded = true
	}
	return dotEnv[name]
}

func envFilePaths() []string {
	var ps []string
	if p := os.Getenv("MOLASEARCH_ENV"); p != "" {
		ps = append(ps, p)
	}
	ps = append(ps, filepath.Join(DataDir(), ".env"))
	if exe, err := os.Executable(); err == nil {
		ps = append(ps, filepath.Join(filepath.Dir(exe), ".env"))
	}
	ps = append(ps, ".env")
	return ps
}

// EnvFile is the .env file that was loaded, if any (for the settings page).
func EnvFile() string {
	envMu.Lock()
	defer envMu.Unlock()
	if !envLoaded {
		loadDotEnvLocked()
		envLoaded = true
	}
	return dotEnvAt
}

// EnvValue reads one name from the loaded .env file, not the process
// environment. The provider platform uses it to show what a field holds.
func EnvValue(name string) string { return envValue(name) }

// EnvTarget is the file the provider platform writes: the first .env that
// exists (the one being read), else <data dir>/.env.
func EnvTarget() string {
	for _, p := range envFilePaths() {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return filepath.Join(DataDir(), ".env")
}

// lookup finds a value by name: process environment, then the .env file,
// then providers.json's search APIs by vendor id.
func lookup(name, vendor string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	if v := envValue(name); v != "" {
		return v
	}
	if vendor != "" {
		for _, a := range provider.StoredSearchAPIs() {
			if a.Vendor == vendor && a.Key != "" && !a.Off {
				return a.Keys()[0]
			}
		}
	}
	return ""
}

// Keys is every key the engine may use, resolved once per call.
type Keys struct {
	TencentSecretID  string
	TencentSecretKey string
	// TencentWSAKey is the Bearer for the vendor's MCP endpoint. It is a
	// separate credential: the TC3 pair answers UnauthorizedOperation there.
	TencentWSAKey string
	Amap          string
	Tavily        string
	Exa           string
	Brave         string
	DeepSeek      string
	Jina          string
	SearXNG       string
	SearXNGURL    string
	EmbedAPIBase  string
	EmbedAPIKey   string
	EmbedModel    string
	RerankAPIBase string
	RerankAPIKey  string
	RerankModel   string
	VisionAPIBase string
	VisionAPIKey  string
	VisionModel   string
}

// ResolveKeys gathers every key from env, .env and providers.json.
func ResolveKeys() Keys {
	return Keys{
		TencentSecretID:  lookup("TENCENT_SECRET_ID", "tencent"),
		TencentSecretKey: lookup("TENCENT_SECRET_KEY", ""),
		TencentWSAKey:    lookup("TENCENT_WSA_API_KEY", ""),
		Amap:             lookup("AMAP_KEY", "amap"),
		Tavily:           lookup("TAVILY_API_KEY", "tavily"),
		Exa:              lookup("EXA_API_KEY", "exa"),
		Brave:            lookup("BRAVE_API_KEY", "brave"),
		DeepSeek:         lookup("DEEPSEEK_API_KEY", "deepseek"),
		Jina:             lookup("JINA_API_KEY", ""),
		SearXNG:          lookup("SEARXNG_API_KEY", "searxng"),
		SearXNGURL:       lookup("SEARXNG_URL", ""),
		EmbedAPIBase:     lookup("MOLA_EMBED_API_BASE", ""),
		EmbedAPIKey:      lookup("MOLA_EMBED_API_KEY", ""),
		EmbedModel:       lookup("MOLA_EMBED_MODEL", ""),
		RerankAPIBase:    lookup("MOLA_RERANK_API_BASE", ""),
		RerankAPIKey:     lookup("MOLA_RERANK_API_KEY", ""),
		RerankModel:      lookup("MOLA_RERANK_MODEL", ""),
		VisionAPIBase:    lookup("MOLA_VISION_API_BASE", ""),
		VisionAPIKey:     lookup("MOLA_VISION_API_KEY", ""),
		VisionModel:      lookup("MOLA_VISION_MODEL", ""),
	}
}

// TencentReady says whether the Tencent Cloud WSA channel can run.
func (k Keys) TencentReady() bool {
	return k.TencentSecretID != "" && k.TencentSecretKey != ""
}

// TencentMCPReady says whether the vendor's MCP endpoint can be asked: it
// takes a WSA API key, not the SecretId/SecretKey pair.
func (k Keys) TencentMCPReady() bool {
	return k.TencentWSAKey != ""
}

// KeyStatus is one key as the settings drawer shows it.
type KeyStatus struct {
	Name      string `json:"name"`
	Env       string `json:"env"`
	Set       bool   `json:"set"`
	Hint      string `json:"hint"`
	WhereFrom string `json:"whereFrom"`
}

// KeyStatuses lists every key the engine reads, masked.
func KeyStatuses() []KeyStatus {
	k := ResolveKeys()
	m := func(s string) string {
		if s == "" {
			return ""
		}
		if len(s) > 8 {
			return s[:4] + "…" + s[len(s)-4:]
		}
		return "…"
	}
	st := func(set bool) string {
		if set {
			return "✅ 已配置"
		}
		return "⭕ 未配置（免Key通道兜底）"
	}
	// mcpState differs from st: without the MCP key the channel still runs
	// over the signed API, so "免Key通道兜底" would be the wrong story.
	mcpState := func(set bool) string {
		if set {
			return "✅ 已配置"
		}
		return "⭕ 未配置（只走 TC3 签名接口）"
	}
	return []KeyStatus{
		{Name: "TENCENT_SECRET_ID", Env: m(k.TencentSecretID), Set: k.TencentSecretID != "", Hint: "腾讯云 WSA SearchPro（wsa.tencentcloudapi.com，X-TC-Version " + tencentWSAVersion() + "）", WhereFrom: st(k.TencentSecretID != "")},
		{Name: "TENCENT_SECRET_KEY", Env: m(k.TencentSecretKey), Set: k.TencentSecretKey != "", Hint: "与 SecretId 成对使用（TC3 签名）", WhereFrom: st(k.TencentSecretKey != "")},
		{Name: "TENCENT_WSA_API_KEY", Env: m(k.TencentWSAKey), Set: k.TencentWSAKey != "", Hint: "元宝 MCP 端点（api.wsa.cloud.tencent.com/Mcp）；与 SecretId/SecretKey 不同，缺失时只走 TC3", WhereFrom: mcpState(k.TencentWSAKey != "")},
		{Name: "AMAP_KEY", Env: m(k.Amap), Set: k.Amap != "", Hint: "高德LBS/气象（无则走 Nominatim+Open-Meteo）", WhereFrom: st(k.Amap != "")},
		{Name: "TAVILY_API_KEY", Env: m(k.Tavily), Set: k.Tavily != "", Hint: "Tavily 搜索（可选增强）", WhereFrom: st(k.Tavily != "")},
		{Name: "EXA_API_KEY", Env: m(k.Exa), Set: k.Exa != "", Hint: "Exa 神经搜索（可选增强）", WhereFrom: st(k.Exa != "")},
		{Name: "BRAVE_API_KEY", Env: m(k.Brave), Set: k.Brave != "", Hint: "Brave 搜索（可选增强）", WhereFrom: st(k.Brave != "")},
		{Name: "MOLA_EMBED_API_BASE/KEY/MODEL", Env: m(k.EmbedAPIBase), Set: k.EmbedAPIBase != "", Hint: "远程 Embedding（无则本地 N-Gram 哈希嵌入）", WhereFrom: st(k.EmbedAPIBase != "")},
		{Name: "MOLA_RERANK_API_BASE/KEY/MODEL", Env: m(k.RerankAPIBase), Set: k.RerankAPIBase != "", Hint: "远程 Reranker（无则 RRF k=60 稠密+BM25 融合）", WhereFrom: st(k.RerankAPIBase != "")},
		{Name: "MOLA_VISION_API_BASE/KEY/MODEL", Env: m(k.VisionAPIBase), Set: k.VisionAPIBase != "", Hint: "图片OCR视觉模型（可选）", WhereFrom: st(k.VisionAPIBase != "")},
	}
}
