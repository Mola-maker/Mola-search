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
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yetone/magpie/internal/agent"
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
		ctx := ragOperationContext(r)
		backends, err := m.Status(ctx)
		if err != nil {
			fail(rw, err)
			return
		}
		records, _ := m.Records(ctx)
		if records == nil {
			records = []searchdet.CanonicalMemoryRecord{}
		}
		habits, _ := m.Habits(ctx)
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
		ctx, cancel := context.WithTimeout(ragOperationContext(r), 120*time.Second)
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
		backends, _ := m.Status(ctx)
		records, _ := m.Records(ctx)
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
		n, err := m.ExportUSMF(ragOperationContext(r), f)
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
		n, err := m.ImportUSMF(ragOperationContext(r), rd)
		if err != nil {
			fail(rw, err)
			return
		}
		records, _ := m.Records(ragOperationContext(r))
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
		if err := m.Insert(ragOperationContext(r), []searchdet.CanonicalMemoryRecord{rec}); err != nil {
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
		hits, err := m.Search(ragOperationContext(r), in.Query, in.TopK)
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
		habits, err := m.Habits(ragOperationContext(r))
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
		files, err := syncHabitsToAgents(ragOperationContext(r), m)
		if err != nil {
			fail(rw, err)
			return
		}
		writeJSON(rw, map[string]any{"updatedFiles": files})
	})

	// The six database panels read one payload: the active corpus, every
	// backend's real status, and each store's own view (relation edges,
	// stored vectors, vault files, SQLite's schema counts, the lexical
	// index). Nothing here is estimated.
	mux.HandleFunc("GET /api/rag/panels", func(rw http.ResponseWriter, r *http.Request) {
		m, err := ragManagerOf()
		if err != nil {
			fail(rw, err)
			return
		}
		data, err := m.Panels(ragOperationContext(r))
		if err != nil {
			fail(rw, err)
			return
		}
		writeJSON(rw, data)
	})

	// The live feed's non-streaming shape: recent real operations, and the
	// agent registry the page draws each caller with.
	mux.HandleFunc("GET /api/rag/operations", func(rw http.ResponseWriter, r *http.Request) {
		m, err := ragManagerOf()
		if err != nil {
			fail(rw, err)
			return
		}
		since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
		operations := m.RecentOperations(40)
		if since > 0 {
			operations = m.OperationsSince(since)
		}
		latest := since
		for _, op := range operations {
			if op.ID > latest {
				latest = op.ID
			}
		}
		writeJSON(rw, map[string]any{
			"operations": operations,
			"agents":     ragAgents(),
			"latest":     latest,
		})
	})

	// The live feed itself: Server-Sent Events, one frame per real operation.
	// The hello frame carries the recent history and the agent registry, so a
	// page that connects late still shows only what really happened.
	mux.HandleFunc("GET /api/rag/events", func(rw http.ResponseWriter, r *http.Request) {
		m, err := ragManagerOf()
		if err != nil {
			fail(rw, err)
			return
		}
		// ResponseController reaches the flusher through whatever wrapper
		// (the web server's gzip pass) sits over the writer.
		control := http.NewResponseController(rw)
		rw.Header().Set("Content-Type", "text/event-stream")
		rw.Header().Set("Cache-Control", "no-cache")
		rw.Header().Set("Connection", "keep-alive")
		rw.Header().Set("X-Accel-Buffering", "no")
		rw.WriteHeader(http.StatusOK)
		if err := writeSSE(rw, "hello", map[string]any{
			"operations": m.RecentOperations(40),
			"agents":     ragAgents(),
		}); err != nil {
			return
		}
		if err := control.Flush(); err != nil {
			return
		}

		ops, unsubscribe := m.SubscribeOperations()
		defer unsubscribe()
		heartbeat := time.NewTicker(20 * time.Second)
		defer heartbeat.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-heartbeat.C:
				if _, err := io.WriteString(rw, ": keep-alive\n\n"); err != nil {
					return
				}
				if control.Flush() != nil {
					return
				}
			case op, open := <-ops:
				if !open {
					return
				}
				if err := writeSSE(rw, "op", op); err != nil {
					return
				}
				if control.Flush() != nil {
					return
				}
			}
		}
	})
}

// ragOperationContext names the caller of an /api/rag call so the live feed
// can draw its agent icon. A caller that names itself (an MCP client, a
// script) is believed; the page calls without the header, and the operation
// is attributed to magpie, which is who really made it.
func ragOperationContext(r *http.Request) context.Context {
	actor := strings.TrimSpace(r.Header.Get("X-Magpie-Actor"))
	return ragstore.WithActor(r.Context(), actor)
}

// ragAgent is one client of the memory bus as the feed draws it: a real
// registry row, its bundled icon and the URL that serves it.
type ragAgent struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Icon    string   `json:"icon"`
	IconURL string   `json:"iconUrl"`
	Aliases []string `json:"aliases,omitempty"`
}

// ragAgents is the agent registry, reduced to what the feed needs. The
// registry is detected from disk, so it is held for a short while: a page
// that polls or reconnects does not re-detect every agent each time.
func ragAgents() []ragAgent {
	ragAgentsMu.Lock()
	defer ragAgentsMu.Unlock()
	if ragAgentsCache != nil && time.Since(ragAgentsAt) < 30*time.Second {
		return ragAgentsCache
	}
	clients := agent.Clients()
	out := make([]ragAgent, 0, len(clients))
	for _, a := range clients {
		out = append(out, ragAgent{
			ID: a.ID, Name: a.Name, Icon: a.Icon, IconURL: ragIconURL(a.Icon),
			Aliases: append([]string(nil), a.Aliases...),
		})
	}
	ragAgentsCache = out
	ragAgentsAt = time.Now()
	return out
}

var (
	ragAgentsMu    sync.Mutex
	ragAgentsCache []ragAgent
	ragAgentsAt    time.Time
)

// ragIconURL resolves a bundled icon name to the file that really exists.
func ragIconURL(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "magpie"
	}
	for _, ext := range []string{"svg", "png"} {
		if _, err := fs.Stat(assets, "assets/icons/"+name+"."+ext); err == nil {
			return "icons/" + name + "." + ext
		}
	}
	return "icons/magpie.svg"
}

// writeSSE writes one named event frame.
func writeSSE(w io.Writer, event string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
	return err
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
	records, err := m.Records(ctx)
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
