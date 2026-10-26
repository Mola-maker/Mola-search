package ragstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/yetone/magpie/internal/searchdet"
	"github.com/yetone/magpie/internal/usmf"
)

// Manager owns every RAG backend and decides which one the app writes to.
// The six backends share one SQLite mirror: the primary store is that mirror
// under the sqlite_hybrid id, and the four remote backends read and write it
// too, so a fact stored while the app was pointed at Postgres is still there
// after a hot swap to the vault. Swapping changes where new writes go and
// which backend answers a search; it never loses a record.
type Manager struct {
	mu       sync.RWMutex
	backends map[string]Store
	activeID string
	// active is read on the search path without taking the write lock, so a
	// hot swap never stalls an in-flight query.
	active atomic.Pointer[Store]

	primary *SQLiteHybridStore
	vault   *MarkdownVaultStore
	keys    searchdet.Keys
	emb     Embedder

	// choiceMu guards the two choices the page makes besides the backend:
	// the reranker applied to search results, and the memory mode that
	// decides whether new facts are written at all.
	choiceMu   sync.RWMutex
	reranker   Reranker
	memoryMode string

	// events is the live operation feed: one record per real read, write,
	// swap or probe this manager performs. Nil only on a hand-built Manager
	// in a test, where note() is a no-op.
	events *eventRing
}

// NewManager opens the primary SQLite store and the vault, registers the four
// remote backends over the primary mirror, and makes sqlite_hybrid active. It
// is idempotent per process: calling it twice opens two Managers over the
// same files, which SQLite's WAL and busy_timeout make safe.
func NewManager(k searchdet.Keys) (*Manager, error) {
	primary, err := OpenSQLite(DefaultSQLitePath())
	if err != nil {
		return nil, err
	}
	vault, err := OpenVault(DefaultVaultDir())
	if err != nil {
		primary.Close()
		return nil, err
	}
	emb := NewEmbedder(k)
	primary.SetEmbedder(emb)
	vault.SetEmbedder(emb)

	m := &Manager{
		backends:   map[string]Store{},
		primary:    primary,
		vault:      vault,
		keys:       k,
		emb:        emb,
		memoryMode: MemoryModePersistent,
		events:     newEventRing(),
	}
	if k.RerankAPIBase != "" {
		m.reranker = &RemoteReranker{
			Base:  strings.TrimRight(k.RerankAPIBase, "/"),
			Key:   k.RerankAPIKey,
			Model: k.RerankModel,
		}
	}
	m.backends[primary.ID()] = primary
	m.backends[vault.ID()] = vault
	m.backends[PostgresID] = NewPostgresStore("", primary)
	m.backends[MilvusID] = NewMilvusStore("", primary)
	m.backends[ElasticsearchID] = NewElasticsearchStore("", primary)
	m.backends[Neo4jID] = NewNeo4jStore("", primary)
	m.setActive(primary.ID())
	return m, nil
}

// Active implements the read side of the active pointer.
func (m *Manager) Active() Store {
	if s := m.active.Load(); s != nil {
		return *s
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.backends[m.activeID]
}

// ActiveID names the backend writes currently go to.
func (m *Manager) ActiveID() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.activeID
}

func (m *Manager) setActive(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.activeID = id
	s := m.backends[id]
	m.active.Store(&s)
}

// Backends returns a copy of the registry, so a caller iterating it can't
// race a hot swap.
func (m *Manager) Backends() map[string]Store {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]Store, len(m.backends))
	for id, s := range m.backends {
		out[id] = s
	}
	return out
}

// Embedder is the model this manager embeds with.
func (m *Manager) Embedder() Embedder { return m.emb }

// Primary is the SQLite store that holds the canonical corpus and the
// habits/stakeout tables.
func (m *Manager) Primary() *SQLiteHybridStore { return m.primary }

// Vault is the Markdown backend, for callers that want the folder itself.
func (m *Manager) Vault() *MarkdownVaultStore { return m.vault }

// Insert writes the records to the active backend and then lets the habit
// learner see their sources: a fact that came from a trusted domain should
// make that domain more trusted the next time it appears.
func (m *Manager) Insert(ctx context.Context, recs []usmf.CanonicalMemoryRecord) error {
	if len(recs) == 0 {
		return nil
	}
	// Ephemeral mode keeps nothing: the promise is that a search can be run
	// without leaving a record behind.
	if m.MemoryMode() == MemoryModeEphemeral {
		return nil
	}
	active := m.Active()
	if active == nil {
		return fmt.Errorf("ragstore: no active backend")
	}
	backend := active.ID()
	started := time.Now()
	if err := active.Upsert(ctx, recs); err != nil {
		m.note(ctx, backend, "write", "memory", 0, false, time.Since(started))
		return err
	}
	m.learnDomains(ctx, recs)
	m.note(ctx, backend, "write", "memory", len(recs), true, time.Since(started))
	return nil
}

// Search runs the hybrid retrieval on the active backend, then applies the
// configured reranker when there is one. A reranker that fails leaves the
// RRF order untouched: a search never fails because of it.
func (m *Manager) Search(ctx context.Context, query string, k int) ([]Hit, error) {
	active := m.Active()
	if active == nil {
		return nil, fmt.Errorf("ragstore: no active backend")
	}
	backend := active.ID()
	started := time.Now()
	qvec, err := m.emb.Embed(ctx, query)
	if err != nil {
		m.note(ctx, backend, "read", "search", 0, false, time.Since(started))
		return nil, err
	}
	hits, err := active.Search(ctx, query, qvec, k)
	if err != nil {
		m.note(ctx, backend, "read", "search", 0, false, time.Since(started))
		return nil, err
	}
	m.note(ctx, backend, "read", "search", len(hits), true, time.Since(started))
	return m.rerank(ctx, query, hits), nil
}

// rerank orders hits by the remote reranker's scores, keeping the RRF order
// when there is no reranker or it answers with an error.
func (m *Manager) rerank(ctx context.Context, query string, hits []Hit) []Hit {
	m.choiceMu.RLock()
	r := m.reranker
	m.choiceMu.RUnlock()
	if r == nil || len(hits) < 2 {
		return hits
	}
	docs := make([]string, len(hits))
	for i, h := range hits {
		docs[i] = documentText(fieldsOf(h.Record.Topic, h.Record.Claim, h.Record.EvidenceText))
	}
	scores, err := r.Rerank(ctx, query, docs)
	if err != nil || len(scores) != len(hits) {
		if err != nil {
			log.Printf("ragstore: reranker %s: %v (keeping RRF order)", r.ID(), err)
		}
		return hits
	}
	out := make([]Hit, len(hits))
	copy(out, hits)
	for i := range out {
		out[i].Rerank = scores[i]
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Rerank > out[j].Rerank })
	return out
}

// Status reports every backend with its real reachability, its real record
// count and a measured latency. The latency is the wall time of one search
// against the primary store, taken now — it is a measurement, not a constant.
func (m *Manager) Status(ctx context.Context) ([]searchdet.DBBackendInfo, error) {
	m.mu.RLock()
	backends := make(map[string]Store, len(m.backends))
	for id, s := range m.backends {
		backends[id] = s
	}
	activeID := m.activeID
	m.mu.RUnlock()

	latency := m.measureLatency(ctx)

	infos := []searchdet.DBBackendInfo{
		{
			ID: SQLiteHybridID, Icon: "🪶", Name: "SQLite 混合库",
			EngineType:   "SQLite + FTS5 + 向量扫描",
			Description:  "主库：记录、向量、关系边与习惯同库，稠密余弦 + BM25 经 RRF 融合；零配置、可随应用离线运行。",
			Capabilities: "混合检索 · 双时序失效 · 关系边 · 习惯学习 · 盯梢持久化",
		},
		{
			ID: MarkdownVaultID, Icon: "📓", Name: "Markdown 笔记库",
			EngineType:   "Markdown + YAML frontmatter",
			Description:  "每条事实一篇 .md，正文可读可编辑，能被任意笔记软件或同步盘接管；读回时重新嵌入并检索。",
			Capabilities: "人可读 · 可编辑 · 可同步 · 混合检索 · 原子写入",
		},
		{
			ID: PostgresID, Icon: "🐘", Name: "PostgreSQL 17 Hybrid",
			EngineType:   "PostgreSQL + pgvector",
			Description:  "TCP 探活真实拨号到 DSN 的 host:port；本模块无 pg 驱动，数据落在同一份 SQLite 镜像，服务器可用时按 DSN 直连探测。",
			Capabilities: "TCP 探活 · 镜像持久化 · 混合检索",
		},
		{
			ID: MilvusID, Icon: "⚡", Name: "Milvus / Zilliz",
			EngineType:   "Milvus v2 REST",
			Description:  "REST 网关 /healthz 探活（19530 为 gRPC，未开 REST 时如实报不可用）；向量数据由镜像承载。",
			Capabilities: "REST 探活 · 镜像持久化 · 混合检索",
		},
		{
			ID: ElasticsearchID, Icon: "🔍", Name: "Elasticsearch 8.x",
			EngineType:   "Elasticsearch REST",
			Description:  "GET / 读取真实版本号，证据文档 PUT 进 molasearch_evidence 索引，查询时把集群 BM25 排名与本地稠密排名再次融合。",
			Capabilities: "版本探测 · 真实索引写入 · 集群 BM25 + 本地稠密融合",
		},
		{
			ID: Neo4jID, Icon: "🕸️", Name: "Neo4j Graphiti",
			EngineType:   "Neo4j HTTP Cypher",
			Description:  "以 RETURN 1 事务探活，检索命中后沿 memory_edges 做 1 跳邻居扩展，让关联事实一并返回。",
			Capabilities: "Cypher 探活 · 关系边 · 1 跳邻居扩展 · 混合检索",
		},
	}

	for i := range infos {
		info := &infos[i]
		s, ok := backends[info.ID]
		if !ok {
			continue
		}
		info.DSN = m.dsnOf(info.ID)
		if c, ok := s.(counter); ok {
			if n, err := c.Count(ctx); err == nil {
				info.RecordCount = n
			}
		} else if recs, err := s.All(ctx); err == nil {
			info.RecordCount = len(recs)
		}
	}

	// The four remote probes run at once: each may wait its full dial
	// timeout on a network that blackholes packets, and a status page that
	// took four timeouts in a row would be unusable. Local backends answer
	// from the same goroutines without a network wait.
	reachable := make([]bool, len(infos))
	probeTook := make([]time.Duration, len(infos))
	var wg sync.WaitGroup
	for i := range infos {
		s, ok := backends[infos[i].ID]
		if !ok {
			continue
		}
		wg.Add(1)
		go func(i int, s Store) {
			defer wg.Done()
			started := time.Now()
			reachable[i] = s.Ping(ctx) == nil
			probeTook[i] = time.Since(started)
		}(i, s)
	}
	wg.Wait()

	for i := range infos {
		info := &infos[i]
		if _, ok := backends[info.ID]; !ok {
			continue
		}
		info.Reachable = reachable[i]
		if reachable[i] {
			info.AvgLatency = latency
		} else {
			// The count and the address are still real; only reachability
			// is missing, and the description says how that backend behaves
			// when it is down.
			info.AvgLatency = "不可用"
		}
		if info.ID == activeID {
			info.Capabilities = "【当前主库】 " + info.Capabilities
		}
		m.note(ctx, info.ID, "probe", "reachability", 0, reachable[i], probeTook[i])
	}
	return infos, nil
}

// measureLatency times one real search against the primary store. With an
// empty corpus there is nothing to time, so the measurement is of the query
// embedding and the scan of zero rows, and it says so by being tiny rather
// than invented.
func (m *Manager) measureLatency(ctx context.Context) string {
	qvec, err := m.emb.Embed(ctx, "latency probe")
	if err != nil {
		return "不可用"
	}
	start := time.Now()
	_, _ = m.primary.Search(ctx, "latency probe", qvec, 10)
	d := time.Since(start)
	return fmt.Sprintf("%.1f ms", float64(d.Microseconds())/1000)
}

func (m *Manager) dsnOf(id string) string {
	switch id {
	case SQLiteHybridID:
		return m.primary.Path()
	case MarkdownVaultID:
		return m.vault.Dir()
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	switch s := m.backends[id].(type) {
	case *PostgresStore:
		return s.DSN
	case *MilvusStore:
		return s.Base
	case *ElasticsearchStore:
		return s.Base
	case *Neo4jStore:
		return s.Base
	}
	return ""
}

// --- habits -------------------------------------------------------------

// habitPattern turns a query into the key a habit is learned under: the
// user's own words, lowercased and with runs of whitespace and punctuation
// collapsed, so "iPhone 17 电池" and "iphone  17 电池!" share a habit while
// two different questions do not. It is deliberately not built from
// Tokenize's n-grams: a habit is read by a person, and "电池 池续 续航" is
// not a pattern anyone recognises.
func habitPattern(query string) string {
	var b strings.Builder
	lastSpace := false
	for _, r := range strings.ToLower(strings.TrimSpace(query)) {
		if unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) {
			if !lastSpace && b.Len() > 0 {
				b.WriteByte(' ')
				lastSpace = true
			}
			continue
		}
		b.WriteRune(r)
		lastSpace = false
	}
	pattern := strings.TrimSpace(b.String())
	if runes := []rune(pattern); len(runes) > 64 {
		pattern = strings.TrimSpace(string(runes[:64]))
	}
	return pattern
}

// LearnFromInvestigation folds one finished investigation into the habits:
// which domains earned trust, which were content farms, and which mode paid
// off for this kind of question. Every number written here comes from the
// citations and verdict of this run.
func (m *Manager) LearnFromInvestigation(ctx context.Context, query string, verdict string, cites []searchdet.Citation) error {
	// Ephemeral mode learns nothing either: a habit is a kept record.
	if m.MemoryMode() == MemoryModeEphemeral {
		return nil
	}
	pattern := habitPattern(query)
	if pattern == "" {
		return errors.New("ragstore: investigation without a query")
	}
	habits, err := m.primary.loadHabits(ctx)
	if err != nil {
		return err
	}
	var h Habit
	found := false
	for _, existing := range habits {
		if existing.Pattern == pattern {
			h, found = existing, true
			break
		}
	}
	if !found {
		h = Habit{Pattern: pattern, ModeAffinity: map[string]float64{}}
	}
	if h.ModeAffinity == nil {
		h.ModeAffinity = map[string]float64{}
	}

	trusted := append([]string(nil), h.TrustedDomains...)
	blocked := append([]string(nil), h.BlockedDomains...)
	for _, c := range cites {
		d := normalizeDomain(c.Domain)
		if d == "" {
			d = normalizeDomain(c.URL)
		}
		if d == "" {
			continue
		}
		switch {
		case c.AuthorityLevel >= 4:
			trusted = pushUnique(trusted, d)
		case c.AuthorityLevel <= 1:
			blocked = pushUnique(blocked, d)
		}
	}
	// A domain that was just seen as a content farm stops being trusted:
	// one list must not contradict the other.
	trusted = removeAll(trusted, blocked)
	h.TrustedDomains = capStrings(trusted, 20)
	h.BlockedDomains = capStrings(blocked, 20)

	for mode, delta := range verdictAffinity(verdict) {
		h.ModeAffinity[mode] = round3(h.ModeAffinity[mode] + delta)
	}
	h.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	return m.primary.saveHabit(ctx, h)
}

// verdictAffinity is which investigation modes a verdict argues for. A
// refutation means the rumour-checking modes earned their keep; a
// confirmation means the authority-seeking ones did; an abstention means
// neither, and the next run should widen the search instead.
func verdictAffinity(verdict string) map[string]float64 {
	switch strings.ToUpper(strings.TrimSpace(verdict)) {
	case "REFUTED", "FALSE", "DEBUNKED", "辟谣":
		return map[string]float64{"rumor_debunk": 0.1, "fact_check": 0.05}
	case "VERIFIED", "TRUE", "CONFIRMED", "证实":
		return map[string]float64{"authority_first": 0.1, "primary_source": 0.05}
	case "ABSTAIN", "UNKNOWN", "存疑":
		return map[string]float64{"wide_recall": 0.1}
	}
	return nil
}

// learnDomains records the domains of freshly stored records without a
// verdict: a record with authority_level >= 4 is a source worth trusting
// again.
func (m *Manager) learnDomains(ctx context.Context, recs []usmf.CanonicalMemoryRecord) {
	var trusted, blocked []string
	for _, r := range recs {
		d := normalizeDomain(r.SourceDomain)
		if d == "" {
			d = normalizeDomain(r.SourceURL)
		}
		if d == "" {
			continue
		}
		switch {
		case r.AuthorityLevel >= 4:
			trusted = pushUnique(trusted, d)
		case r.AuthorityLevel > 0 && r.AuthorityLevel <= 1:
			blocked = pushUnique(blocked, d)
		}
	}
	if len(trusted) == 0 && len(blocked) == 0 {
		return
	}
	pattern := habitPattern(recs[0].Topic)
	if pattern == "" {
		pattern = "_sources"
	}
	habits, err := m.primary.loadHabits(ctx)
	if err != nil {
		return
	}
	var h Habit
	found := false
	for _, existing := range habits {
		if existing.Pattern == pattern {
			h, found = existing, true
			break
		}
	}
	if !found {
		h = Habit{Pattern: pattern, ModeAffinity: map[string]float64{}}
	}
	for _, d := range trusted {
		h.TrustedDomains = pushUnique(h.TrustedDomains, d)
	}
	for _, d := range blocked {
		h.BlockedDomains = pushUnique(h.BlockedDomains, d)
	}
	h.TrustedDomains = removeAll(h.TrustedDomains, h.BlockedDomains)
	h.TrustedDomains = capStrings(h.TrustedDomains, 20)
	h.BlockedDomains = capStrings(h.BlockedDomains, 20)
	h.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	_ = m.primary.saveHabit(ctx, h)
}

// Habits returns every learned habit, newest first.
func (m *Manager) Habits(ctx context.Context) ([]Habit, error) {
	started := time.Now()
	habits, err := m.primary.loadHabits(ctx)
	if err != nil {
		m.note(ctx, SQLiteHybridID, "read", "habits", 0, false, time.Since(started))
		return nil, err
	}
	sort.SliceStable(habits, func(i, j int) bool {
		if habits[i].UpdatedAt != habits[j].UpdatedAt {
			return habits[i].UpdatedAt > habits[j].UpdatedAt
		}
		return habits[i].Pattern < habits[j].Pattern
	})
	m.note(ctx, SQLiteHybridID, "read", "habits", len(habits), true, time.Since(started))
	return habits, nil
}

// normalizeDomain reduces a domain or a URL to the bare host a habit list
// keeps: lowercase, no scheme, no path, no leading www.
func normalizeDomain(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return ""
	}
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	if i := strings.Index(s, "@"); i >= 0 {
		s = s[i+1:]
	}
	if i := strings.Index(s, ":"); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimPrefix(s, "www.")
	return s
}

func pushUnique(list []string, v string) []string {
	if v == "" {
		return list
	}
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

func removeAll(list, drop []string) []string {
	if len(drop) == 0 {
		return list
	}
	out := list[:0]
	for _, x := range list {
		keep := true
		for _, d := range drop {
			if x == d {
				keep = false
				break
			}
		}
		if keep {
			out = append(out, x)
		}
	}
	return out
}

func capStrings(list []string, n int) []string {
	if len(list) > n {
		return list[:n]
	}
	return list
}

func round3(f float64) float64 {
	return float64(int64(f*1000+0.5)) / 1000
}

// --- lifecycle ----------------------------------------------------------

// Close closes every backend's own resources and the shared mirror once.
// The remote backends share the mirror, so closing it through them would
// close it six times; it is closed here instead.
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var firstErr error
	if err := m.vault.Close(); err != nil {
		firstErr = err
	}
	if err := m.primary.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	m.backends = map[string]Store{}
	m.active.Store(nil)
	return firstErr
}

// backupDir is where HotSwap writes its pre-switch snapshot.
func backupDir() string {
	return searchdet.DataDir()
}

// writeJSONL writes lines to a file through a temp file and a rename, so a
// reader never sees a partial export.
func writeJSONL(path string, lines []string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".export-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	for _, line := range lines {
		if _, err := tmp.WriteString(line); err != nil {
			tmp.Close()
			os.Remove(name)
			return err
		}
		if _, err := tmp.WriteString("\n"); err != nil {
			tmp.Close()
			os.Remove(name)
			return err
		}
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

// marshalRecord is the one JSON encoding used by both the USMF export and
// the backup, so the two files are byte-identical for identical records.
func marshalRecord(r usmf.CanonicalMemoryRecord) (string, error) {
	b, err := json.Marshal(r)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
