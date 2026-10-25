package ragstore

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/searchdet"
	"github.com/yetone/magpie/internal/usmf"
)

// memRecord is one fact in the shape an investigation stores.
func memRecord(id, claim string) usmf.CanonicalMemoryRecord {
	return usmf.CanonicalMemoryRecord{
		MemoryID:       id,
		Topic:          claim,
		Claim:          claim,
		EvidenceText:   claim + "，官方通报与实测数据一致。",
		Verdict:        "VERIFIED",
		RelationEdge:   "SUPPORTS",
		ValidFrom:      time.Now().Format(time.RFC3339),
		SourceURL:      "https://example.gov.cn/" + id,
		SourceDomain:   "example.gov.cn",
		AuthorityLevel: 4,
		EmbeddingModel: "local-ngram-1024",
	}
}

// newTestManager opens a manager over a throwaway data directory.
func newTestManager(t *testing.T) *Manager {
	t.Helper()
	t.Setenv("MOLASEARCH_DATA", t.TempDir())
	m, err := NewManager(searchdet.Keys{})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })
	return m
}

// testStore is an in-memory Store whose writes can be paused, so a test can
// prove reads keep working while a hot swap writes.
type testStore struct {
	id   string
	mu   sync.Mutex
	recs []usmf.CanonicalMemoryRecord

	started chan struct{}
	release chan struct{}
	once    sync.Once
	drop    map[string]bool
}

func (s *testStore) ID() string                   { return s.id }
func (s *testStore) Ping(_ context.Context) error { return nil }
func (s *testStore) Close() error                 { return nil }
func (s *testStore) Search(_ context.Context, _ string, _ []float32, _ int) ([]Hit, error) {
	return nil, nil
}

func (s *testStore) Upsert(_ context.Context, recs []usmf.CanonicalMemoryRecord) error {
	if s.started != nil {
		s.once.Do(func() { close(s.started) })
		<-s.release
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range recs {
		if s.drop[r.MemoryID] {
			continue
		}
		replaced := false
		for i := range s.recs {
			if s.recs[i].MemoryID == r.MemoryID {
				s.recs[i] = r
				replaced = true
				break
			}
		}
		if !replaced {
			s.recs = append(s.recs, r)
		}
	}
	return nil
}

func (s *testStore) All(_ context.Context) ([]usmf.CanonicalMemoryRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]usmf.CanonicalMemoryRecord, len(s.recs))
	copy(out, s.recs)
	return out, nil
}

// register adds a backend to a manager, as a hot swap target.
func register(m *Manager, s Store) {
	m.mu.Lock()
	m.backends[s.ID()] = s
	m.mu.Unlock()
}

// TestHotSwapKeepsReadsServingWhileWriting is the zero-downtime promise: a
// search that arrives while the target is being written answers from the
// source, and the active pointer does not move until the copy is verified.
func TestHotSwapKeepsReadsServingWhileWriting(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()
	if err := m.Insert(ctx, []usmf.CanonicalMemoryRecord{
		memRecord("mem_1", "量子计算实验突破"),
		memRecord("mem_2", "量子纠错码进展"),
	}); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	target := &testStore{id: "slow_target", started: make(chan struct{}), release: make(chan struct{})}
	register(m, target)

	done := make(chan error, 1)
	go func() {
		_, err := m.HotSwap(ctx, target.ID(), &LocalHashEmbedder{Dims: DefaultDims})
		done <- err
	}()

	select {
	case <-target.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the hot swap never started writing the target")
	}

	// The write is paused here. A read must still answer, from the source.
	searchCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	hits, err := m.Search(searchCtx, "量子纠缠实验", 5)
	if err != nil {
		t.Fatalf("search during the dual-write failed: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("search during the dual-write returned no hits")
	}
	if got := m.ActiveID(); got != SQLiteHybridID {
		t.Fatalf("active moved to %q before the copy was verified", got)
	}

	close(target.release)
	if err := <-done; err != nil {
		t.Fatalf("hot swap: %v", err)
	}
	if got := m.ActiveID(); got != target.ID() {
		t.Fatalf("active is %q, want %q", got, target.ID())
	}
}

// TestHotSwapRefusesACorpusThatDoesNotMatch makes the switch fail closed: a
// target that lost a record must not become active, and the source keeps
// answering.
func TestHotSwapRefusesACorpusThatDoesNotMatch(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()
	if err := m.Insert(ctx, []usmf.CanonicalMemoryRecord{
		memRecord("mem_1", "第一件事"),
		memRecord("mem_2", "第二件事"),
	}); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	target := &testStore{id: "lossy_target", drop: map[string]bool{"mem_2": true}}
	register(m, target)

	if _, err := m.HotSwap(ctx, target.ID(), &LocalHashEmbedder{Dims: DefaultDims}); err == nil {
		t.Fatal("a target missing a record was accepted")
	}
	if got := m.ActiveID(); got != SQLiteHybridID {
		t.Fatalf("active moved to %q after a failed verification", got)
	}
	hits, err := m.Search(ctx, "第一件事", 5)
	if err != nil || len(hits) == 0 {
		t.Fatalf("the source stopped answering after the refused swap: %v, %d hits", err, len(hits))
	}
	// The pre-swap corpus was written to a backup before anything moved.
	entries, err := os.ReadDir(os.Getenv("MOLASEARCH_DATA"))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "backup_sqlite_hybrid_") && strings.HasSuffix(e.Name(), ".magpie-rag.jsonl") {
			found = true
		}
	}
	if !found {
		t.Error("no USMF backup was written before the swap")
	}
}

// TestHotSwapSwitchesWritesToTheVault proves the switch is real: after a
// swap to the Markdown vault, a new fact is a .md file and is not in the
// SQLite rows.
func TestHotSwapSwitchesWritesToTheVault(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()
	if err := m.Insert(ctx, []usmf.CanonicalMemoryRecord{
		memRecord("mem_1", "迁移前的记录"),
	}); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	report, err := m.HotSwap(ctx, MarkdownVaultID, &LocalHashEmbedder{Dims: DefaultDims})
	if err != nil {
		t.Fatalf("HotSwap: %v", err)
	}
	if report.Records != 1 || report.SHA256 == "" {
		t.Fatalf("swap report is not measured: %+v", report)
	}
	if got := m.ActiveID(); got != MarkdownVaultID {
		t.Fatalf("active is %q", got)
	}
	if err := m.Insert(ctx, []usmf.CanonicalMemoryRecord{
		memRecord("mem_after", "切换之后写入的事实"),
	}); err != nil {
		t.Fatalf("Insert after swap: %v", err)
	}
	vaultRecs, err := m.Vault().All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !hasRecord(vaultRecs, "mem_after") {
		t.Fatal("the new record is not in the vault")
	}
	primaryRecs, err := m.Primary().All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if hasRecord(primaryRecs, "mem_after") {
		t.Fatal("the new record was also written to SQLite after the swap")
	}
	files, err := filepath.Glob(filepath.Join(m.Vault().Dir(), "*.md"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no vault files: %v", err)
	}
	hits, err := m.Search(ctx, "切换之后写入的事实", 5)
	if err != nil || len(hits) == 0 {
		t.Fatalf("search after the swap: %v, %d hits", err, len(hits))
	}
	if hits[0].Record.MemoryID != "mem_after" {
		t.Fatalf("search did not rank the new vault record first: %s", hits[0].Record.MemoryID)
	}
}

// TestEphemeralModeKeepsNothing is the zero-retention promise of the privacy
// modes.
func TestEphemeralModeKeepsNothing(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()
	if err := m.SetMemoryMode(MemoryModeEphemeral); err != nil {
		t.Fatal(err)
	}
	if err := m.Insert(ctx, []usmf.CanonicalMemoryRecord{memRecord("mem_eph", "临时查询")}); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	recs, err := m.Primary().All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if hasRecord(recs, "mem_eph") {
		t.Fatal("ephemeral mode wrote a record")
	}
	if err := m.LearnFromInvestigation(ctx, "某个查询", "VERIFIED", nil); err != nil {
		t.Fatalf("LearnFromInvestigation: %v", err)
	}
	habits, err := m.Habits(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(habits) != 0 {
		t.Fatalf("ephemeral mode learned %d habits", len(habits))
	}
	if err := m.SetMemoryMode("something-else"); err == nil {
		t.Fatal("an unknown memory mode was accepted")
	}
}

func hasRecord(recs []usmf.CanonicalMemoryRecord, id string) bool {
	for _, r := range recs {
		if r.MemoryID == id {
			return true
		}
	}
	return false
}
