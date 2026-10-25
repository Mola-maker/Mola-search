// The RAG Store page's API: six real backends, hybrid search with its three
// scores, the three-phase hot swap, USMF cold export/import, the learned
// habits and the agent-file sync.
package gui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/yetone/magpie/internal/library"
	"github.com/yetone/magpie/internal/ragstore"
	"github.com/yetone/magpie/internal/searchdet"
)

var (
	ragMu      sync.Mutex
	ragManager *ragstore.Manager
	ragInitErr error
)

// ragManagerOf is the process's one manager, opened on first use.
func ragManagerOf() (*ragstore.Manager, error) {
	ragMu.Lock()
	defer ragMu.Unlock()
	if ragManager != nil {
		return ragManager, nil
	}
	if ragInitErr != nil {
		return nil, ragInitErr
	}
	m, err := ragstore.NewManager(searchdet.ResolveKeys())
	if err != nil {
		ragInitErr = err
		return nil, err
	}
	ragManager = m
	return m, nil
}

// ragStoreRoutes registers the page's endpoints.
func ragStoreRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/rag/status", func(rw http.ResponseWriter, r *http.Request) {
		m, err := ragManagerOf()
		if err != nil {
			fail(rw, err)
			return
		}
		backends, err := m.Status(r.Context())
		if err != nil {
			fail(rw, err)
			return
		}
		records, _ := m.Active().All(r.Context())
		if records == nil {
			records = []searchdet.CanonicalMemoryRecord{}
		}
		habits, _ := m.Habits(r.Context())
		if habits == nil {
			habits = []ragstore.Habit{}
		}
		writeJSON(rw, map[string]any{
			"activeDbId":     m.Active().ID(),
			"embeddingModel": m.EmbeddingModel(),
			"rerankerModel":  m.RerankerModel(),
			"memoryMode":     m.MemoryMode(),
			"backends":       backends,
			"records":        records,
			"habits":         habits,
		})
	})

	mux.HandleFunc("POST /api/rag/hotswap", func(rw http.ResponseWriter, r *http.Request) {
		var in struct {
			TargetDbID     string `json:"targetDbId"`
			EmbeddingModel string `json:"embeddingModel"`
			RerankerModel  string `json:"rerankerModel"`
			MemoryMode     string `json:"memoryMode"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in); err != nil {
			fail(rw, err)
			return
		}
		m, err := ragManagerOf()
		if err != nil {
			fail(rw, err)
			return
		}
		emb, err := embedderFor(in.EmbeddingModel)
		if err != nil {
			fail(rw, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
		defer cancel()
		report, err := m.HotSwap(ctx, in.TargetDbID, emb)
		if err != nil {
			fail(rw, err)
			return
		}
		// The reranker and the memory mode are switched with the backend:
		// they are the other two choices the same page makes.
		if in.RerankerModel != "" {
			if err := m.SetReranker(in.RerankerModel); err != nil {
				fail(rw, err)
				return
			}
		}
		if in.MemoryMode != "" {
			if err := m.SetMemoryMode(in.MemoryMode); err != nil {
				fail(rw, err)
				return
			}
		}
		backends, _ := m.Status(r.Context())
		records, _ := m.Active().All(r.Context())
		writeJSON(rw, map[string]any{
			"activeDbId": m.Active().ID(), "backends": backends, "records": records, "swap": report,
			"embeddingModel": m.EmbeddingModel(), "rerankerModel": m.RerankerModel(),
			"memoryMode": m.MemoryMode(),
		})
	})

	mux.HandleFunc("POST /api/rag/coldswap/export", func(rw http.ResponseWriter, r *http.Request) {
		m, err := ragManagerOf()
		if err != nil {
			fail(rw, err)
			return
		}
		path := filepath.Join(searchdet.DataDir(), fmt.Sprintf("coldswap_%s.magpie-rag.jsonl", time.Now().Format("20060102_150405")))
		f, err := os.Create(path)
		if err != nil {
			fail(rw, err)
			return
		}
		n, err := m.ExportUSMF(r.Context(), f)
		cerr := f.Close()
		if err != nil {
			fail(rw, err)
			return
		}
		if cerr != nil {
			fail(rw, cerr)
			return
		}
		writeJSON(rw, map[string]any{"bundlePath": path, "recordCount": n})
	})

	mux.HandleFunc("POST /api/rag/coldswap/import", func(rw http.ResponseWriter, r *http.Request) {
		var in struct {
			JSONL string `json:"jsonl"`
			Path  string `json:"path"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 32<<20)).Decode(&in); err != nil {
			fail(rw, err)
			return
		}
		m, err := ragManagerOf()
		if err != nil {
			fail(rw, err)
			return
		}
		var rd io.Reader
		if in.JSONL != "" {
			rd = strings.NewReader(in.JSONL)
		} else if in.Path != "" {
			f, err := os.Open(in.Path)
			if err != nil {
				fail(rw, err)
				return
			}
			defer f.Close()
			rd = f
		} else {
			fail(rw, fmt.Errorf("send jsonl or path"))
			return
		}
		n, err := m.ImportUSMF(r.Context(), rd)
		if err != nil {
			fail(rw, err)
			return
		}
		records, _ := m.Active().All(r.Context())
		writeJSON(rw, map[string]any{"imported": n, "records": records})
	})

	mux.HandleFunc("POST /api/rag/records", func(rw http.ResponseWriter, r *http.Request) {
		var rec searchdet.CanonicalMemoryRecord
		if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&rec); err != nil {
			fail(rw, err)
			return
		}
		if rec.MemoryID == "" {
			rec.MemoryID = fmt.Sprintf("mem_%d", time.Now().UnixNano())
		}
		if rec.Topic == "" || rec.EvidenceText == "" {
			fail(rw, fmt.Errorf("topic and evidenceText are required"))
			return
		}
		if rec.ValidFrom == "" {
			rec.ValidFrom = time.Now().Format(time.RFC3339)
		}
		if rec.EmbeddingModel == "" {
			rec.EmbeddingModel = "local-ngram-1024"
		}
		m, err := ragManagerOf()
		if err != nil {
			fail(rw, err)
			return
		}
		if err := m.Insert(r.Context(), []searchdet.CanonicalMemoryRecord{rec}); err != nil {
			fail(rw, err)
			return
		}
		writeJSON(rw, map[string]any{"ok": true, "record": rec})
	})

	mux.HandleFunc("POST /api/rag/search", func(rw http.ResponseWriter, r *http.Request) {
		var in struct {
			Query string `json:"query"`
			TopK  int    `json:"topK"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in); err != nil {
			fail(rw, err)
			return
		}
		if in.TopK <= 0 {
			in.TopK = 5
		}
		m, err := ragManagerOf()
		if err != nil {
			fail(rw, err)
			return
		}
		started := time.Now()
		hits, err := m.Search(r.Context(), in.Query, in.TopK)
		if err != nil {
			fail(rw, err)
			return
		}
		if hits == nil {
			hits = []ragstore.Hit{}
		}
		writeJSON(rw, map[string]any{"hits": hits, "tookMs": time.Since(started).Milliseconds()})
	})

	mux.HandleFunc("GET /api/rag/habits", func(rw http.ResponseWriter, r *http.Request) {
		m, err := ragManagerOf()
		if err != nil {
			fail(rw, err)
			return
		}
		habits, err := m.Habits(r.Context())
		if err != nil {
			fail(rw, err)
			return
		}
		if habits == nil {
			habits = []ragstore.Habit{}
		}
		writeJSON(rw, map[string]any{"habits": habits})
	})

	mux.HandleFunc("POST /api/rag/habits/sync-agent-files", func(rw http.ResponseWriter, r *http.Request) {
		m, err := ragManagerOf()
		if err != nil {
			fail(rw, err)
			return
		}
		files, err := syncHabitsToAgents(r.Context(), m)
		if err != nil {
			fail(rw, err)
			return
		}
		writeJSON(rw, map[string]any{"updatedFiles": files})
	})
}

// embedderFor picks the model a hot swap computes vectors with. The local
// deterministic embedder is always available; a remote model needs the
// endpoint it lives on, and asking for one without it is an error rather
// than a silent fallback to another model (the vectors would not match).
func embedderFor(model string) (ragstore.Embedder, error) {
	k := searchdet.ResolveKeys()
	if model == "" || model == "local-ngram-1024" {
		return &ragstore.LocalHashEmbedder{Dims: 1024}, nil
	}
	if k.EmbedAPIBase == "" {
		return nil, fmt.Errorf("嵌入模型 %q 需要 MOLA_EMBED_API_BASE，未配置时只能用 local-ngram-1024", model)
	}
	if k.EmbedModel == "" {
		k.EmbedModel = model
	}
	emb := ragstore.NewEmbedder(k)
	if emb.ID() != model {
		return nil, fmt.Errorf("已配置的嵌入模型是 %q，不是 %q", emb.ID(), model)
	}
	return emb, nil
}

// syncHabitsToAgents writes the P0/P1 memory index and the learned habits
// into each installed agent's instructions file, inside a managed block so
// a second sync replaces it and nothing of the user's is lost.
func syncHabitsToAgents(ctx context.Context, m *ragstore.Manager) ([]string, error) {
	records, err := m.Active().All(ctx)
	if err != nil {
		return nil, err
	}
	habits, _ := m.Habits(ctx)
	block := memoryIndexBlock(records, habits)
	var updated []string
	for _, t := range library.Targets() {
		if t.Gone || t.Instructions == "" {
			continue
		}
		if err := upsertManagedBlock(t.Instructions, block); err != nil {
			return updated, fmt.Errorf("%s: %w", t.Instructions, err)
		}
		updated = append(updated, t.Instructions)
	}
	if len(updated) == 0 {
		return nil, fmt.Errorf("no agent instructions file found")
	}
	return updated, nil
}

// memoryIndexBlock is the compact index: at most 12 records and 6 habits,
// so the block stays well under the 150-line budget.
func memoryIndexBlock(records []searchdet.CanonicalMemoryRecord, habits []ragstore.Habit) string {
	sort.Slice(records, func(i, j int) bool { return records[i].ValidFrom > records[j].ValidFrom })
	var b strings.Builder
	b.WriteString("<!-- molasearch:begin (managed; edits inside are replaced) -->\n")
	b.WriteString("## MolaSearch 记忆索引 (P0/P1)\n\n")
	b.WriteString("调用 `magpie_investigate` 前先看这里：已核验过的结论与来源。\n\n")
	n := len(records)
	if n > 12 {
		n = 12
	}
	for _, r := range records[:n] {
		fmt.Fprintf(&b, "- **%s** → `%s`（%s，权威 %d）%s\n", r.Topic, r.Verdict, r.SourceDomain, r.AuthorityLevel, r.SourceURL)
	}
	if len(habits) > 0 {
		b.WriteString("\n### L3 搜索习惯\n\n")
		hn := len(habits)
		if hn > 6 {
			hn = 6
		}
		for _, h := range habits[:hn] {
			fmt.Fprintf(&b, "- `%s`: 信任 %s；屏蔽 %s\n", h.Pattern,
				strings.Join(firstN(h.TrustedDomains, 3), ", "),
				strings.Join(firstN(h.BlockedDomains, 3), ", "))
		}
	}
	b.WriteString("\n<!-- molasearch:end -->\n")
	return b.String()
}

func firstN(s []string, n int) []string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// upsertManagedBlock replaces the managed block in path, or appends it.
func upsertManagedBlock(path, block string) error {
	const begin, end = "<!-- molasearch:begin", "<!-- molasearch:end -->"
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	s := string(b)
	if i := strings.Index(s, begin); i >= 0 {
		if j := strings.Index(s[i:], end); j >= 0 {
			s = s[:i] + block + s[i+j+len(end):]
		} else {
			s = s[:i] + block
		}
	} else {
		if s != "" && !strings.HasSuffix(s, "\n") {
			s += "\n"
		}
		s += "\n" + block
	}
	var buf bytes.Buffer
	buf.WriteString(s)
	return writeFileAtomic(path, buf.Bytes())
}
