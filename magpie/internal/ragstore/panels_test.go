package ragstore

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/searchdet"
	"github.com/yetone/magpie/internal/usmf"
)

// panelTestManager opens a manager over a throwaway data directory. It never
// dials a real server on the search path; Status probes the configured
// addresses and records what it found, which is what the tests assert.
func panelTestManager(t *testing.T) *Manager {
	t.Helper()
	t.Setenv("MOLASEARCH_DATA", t.TempDir())
	m, err := NewManager(searchdet.Keys{})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })
	return m
}

func panelRecords() []usmf.CanonicalMemoryRecord {
	return []usmf.CanonicalMemoryRecord{
		{
			MemoryID: "mem_1", Topic: "battery chemistry", Claim: "LFP cells last longer",
			EvidenceText: "A ten-year fleet study found LFP cells kept 80% capacity.",
			Verdict:      "VERIFIED", RelationEdge: "SUPERSEDES:mem_2",
			ValidFrom: "2026-01-01T00:00:00Z", SourceDomain: "example.org",
			SourceURL: "https://example.org/study", AuthorityLevel: 4,
		},
		{
			MemoryID: "mem_2", Topic: "battery cost", Claim: "Pack prices fell below $100/kWh",
			EvidenceText: "BloombergNEF put the 2024 volume-weighted pack price at $115/kWh.",
			Verdict:      "UNVERIFIED", RelationEdge: "CONTRADICTS:mem_3",
			ValidFrom: "2025-01-01T00:00:00Z", InvalidAt: "2026-06-01T00:00:00Z",
			SourceDomain: "example.net", AuthorityLevel: 2,
		},
	}
}

func TestPanelsReadRealGraphVectorsDocumentsAndSchema(t *testing.T) {
	m := panelTestManager(t)
	ctx := context.Background()
	recs := panelRecords()
	if err := m.Insert(ctx, recs); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	// The vault is a second real backend: write the same records so the
	// document panel has files to read.
	if err := m.Vault().Upsert(ctx, recs); err != nil {
		t.Fatalf("vault Upsert: %v", err)
	}

	data, err := m.Panels(ctx)
	if err != nil {
		t.Fatalf("Panels: %v", err)
	}
	if data.ActiveDBID != SQLiteHybridID {
		t.Fatalf("active = %q", data.ActiveDBID)
	}
	if len(data.Records) != 2 {
		t.Fatalf("records = %d, want 2", len(data.Records))
	}

	// Graph: two records, two relation edges, both resolved inside the corpus.
	if !data.Graph.Available {
		t.Fatalf("graph unavailable: %s", data.Graph.Error)
	}
	if len(data.Graph.Nodes) != 2 {
		t.Fatalf("graph nodes = %d, want 2", len(data.Graph.Nodes))
	}
	want := map[string]bool{"mem_1|mem_2|SUPERSEDES": false, "mem_2|mem_3|CONTRADICTS": false}
	for _, e := range data.Graph.Edges {
		key := e.Src + "|" + e.Dst + "|" + e.Type
		if _, ok := want[key]; ok {
			want[key] = true
		}
	}
	for k, seen := range want {
		if !seen {
			t.Errorf("relation edge %s missing from %+v", k, data.Graph.Edges)
		}
	}
	// mem_3 is named by an edge but is not a stored record: the panel says
	// how many edges point outside the corpus instead of inventing a node.
	if data.Graph.ExternalEdges != 1 {
		t.Errorf("external edges = %d, want 1", data.Graph.ExternalEdges)
	}

	// Vectors: both stored, projected onto three dimensions with real spread.
	if !data.Vectors.Available {
		t.Fatalf("vectors unavailable: %s", data.Vectors.Error)
	}
	if data.Vectors.Stored != 2 || data.Vectors.Computed != 0 {
		t.Errorf("vector sources = %d stored, %d computed", data.Vectors.Stored, data.Vectors.Computed)
	}
	if len(data.Vectors.Points) != 2 || len(data.Vectors.Axes) != 3 {
		t.Fatalf("points=%d axes=%d", len(data.Vectors.Points), len(data.Vectors.Axes))
	}
	if data.Vectors.Dims != DefaultDims {
		t.Errorf("dims = %d, want %d", data.Vectors.Dims, DefaultDims)
	}
	if len(data.Vectors.Models) != 1 || data.Vectors.Models[0] != "local-ngram-1024" {
		t.Errorf("vector models = %v", data.Vectors.Models)
	}
	for _, p := range data.Vectors.Points {
		for _, v := range []float64{p.X, p.Y, p.Z} {
			if math.IsNaN(v) || v < -1 || v > 1 {
				t.Errorf("projection %v outside [-1,1]", v)
			}
		}
	}
	for _, a := range data.Vectors.Axes {
		if a.Variance <= 0 {
			t.Errorf("axis %d variance = %v, want > 0", a.Dim, a.Variance)
		}
	}

	// Documents: the two real note files the vault wrote.
	if !data.Documents.Available {
		t.Fatalf("documents unavailable: %s", data.Documents.Error)
	}
	if len(data.Documents.Notes) != 2 {
		t.Fatalf("notes = %d, want 2: %+v", len(data.Documents.Notes), data.Documents.Notes)
	}
	for _, note := range data.Documents.Notes {
		if note.Bytes <= 0 {
			t.Errorf("%s has %d bytes", note.File, note.Bytes)
		}
		if note.MemoryID == "" {
			t.Errorf("%s has no memory id", note.File)
		}
	}

	// Relational: the SQLite schema's own counts, not a page-side tally.
	if !data.Relational.Available {
		t.Fatalf("relational unavailable: %s", data.Relational.Error)
	}
	byTable := map[string]TableInfo{}
	for _, table := range data.Relational.Tables {
		byTable[table.Name] = table
	}
	if got := byTable["memory_records"].Rows; got != 2 {
		t.Errorf("memory_records rows = %d, want 2", got)
	}
	if got := byTable["memory_vectors"].Rows; got != 2 {
		t.Errorf("memory_vectors rows = %d, want 2", got)
	}
	if got := byTable["memory_edges"].Rows; got != 2 {
		t.Errorf("memory_edges rows = %d, want 2", got)
	}
	if len(byTable["memory_records"].Columns) == 0 {
		t.Error("memory_records has no columns")
	}
	verdicts := map[string]int{}
	for _, v := range data.Relational.ByVerdict {
		verdicts[v.Verdict] = v.Count
	}
	if verdicts["VERIFIED"] != 1 || verdicts["UNVERIFIED"] != 1 {
		t.Errorf("verdict counts = %v", verdicts)
	}
	// mem_2 is expired (invalid_at in the past); mem_1 is current.
	if data.Relational.Expired < 1 {
		t.Errorf("expired = %d, want at least 1", data.Relational.Expired)
	}
	if data.Relational.Current < 1 {
		t.Errorf("current = %d, want at least 1", data.Relational.Current)
	}

	// Lexical: the index reports the same rows and names its embedder.
	if !data.Lexical.Available {
		t.Fatalf("lexical unavailable: %s", data.Lexical.Error)
	}
	if data.Lexical.Records != 2 || data.Lexical.Vectors != 2 || data.Lexical.Edges != 2 {
		t.Errorf("lexical counts = %+v", data.Lexical)
	}
	if data.Lexical.EmbeddingModel == "" || data.Lexical.Dims != DefaultDims {
		t.Errorf("lexical model = %q dims = %d", data.Lexical.EmbeddingModel, data.Lexical.Dims)
	}

	// Status now carries the probe's boolean, so a page never has to parse
	// the Chinese latency string to know a backend is down.
	foundSQLite := false
	for _, b := range data.Backends {
		if b.ID == SQLiteHybridID {
			foundSQLite = true
			if !b.Reachable {
				t.Errorf("sqlite reachable = false")
			}
		}
	}
	if !foundSQLite {
		t.Fatal("status did not report the sqlite backend")
	}
}

func TestPanelsSayWhatIsMissingInsteadOfInventingIt(t *testing.T) {
	m := panelTestManager(t)
	data, err := m.Panels(context.Background())
	if err != nil {
		t.Fatalf("Panels: %v", err)
	}
	if data.Vectors.Available {
		t.Errorf("an empty corpus reported a vector space: %+v", data.Vectors)
	}
	if data.Vectors.Error != "fewer than two vectors" {
		t.Errorf("vector empty-state error = %q", data.Vectors.Error)
	}
	if len(data.Vectors.Points) != 0 {
		t.Errorf("invented %d vector points", len(data.Vectors.Points))
	}
	if !data.Graph.Available || len(data.Graph.Nodes) != 0 || len(data.Graph.Edges) != 0 {
		t.Errorf("empty graph = %+v", data.Graph)
	}
	if !data.Documents.Available || len(data.Documents.Notes) != 0 {
		t.Errorf("empty vault = %+v", data.Documents)
	}
	if !data.Relational.Available {
		t.Errorf("empty relational = %+v", data.Relational)
	}
	if !data.Lexical.Available || data.Lexical.Records != 0 {
		t.Errorf("empty lexical = %+v", data.Lexical)
	}
}

func TestPanelsComputeVectorsWhenTheActiveBackendKeepsNone(t *testing.T) {
	m := panelTestManager(t)
	ctx := context.Background()
	recs := panelRecords()
	// The vault keeps no vectors on disk; the primary mirror never saw these
	// records. The panel re-embeds them and says so.
	if err := m.Vault().Upsert(ctx, recs); err != nil {
		t.Fatalf("vault Upsert: %v", err)
	}
	m.setActive(MarkdownVaultID)
	data, err := m.Panels(ctx)
	if err != nil {
		t.Fatalf("Panels: %v", err)
	}
	if data.ActiveDBID != MarkdownVaultID {
		t.Fatalf("active = %q", data.ActiveDBID)
	}
	if !data.Vectors.Available {
		t.Fatalf("vectors unavailable: %s", data.Vectors.Error)
	}
	if data.Vectors.Computed != 2 || data.Vectors.Stored != 0 {
		t.Errorf("vector sources = %d stored, %d computed", data.Vectors.Stored, data.Vectors.Computed)
	}
	// The vault's note files are still real documents.
	if len(data.Documents.Notes) != 2 {
		t.Errorf("notes = %d, want 2", len(data.Documents.Notes))
	}
}

func TestOperationsAreRealAndCarryTheirActor(t *testing.T) {
	m := panelTestManager(t)
	ctx := WithActor(context.Background(), "MCP/test-client 1.0")
	recs := panelRecords()
	if err := m.Insert(ctx, recs); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	latest := int64(0)
	for _, op := range m.RecentOperations(0) {
		if op.ID > latest {
			latest = op.ID
		}
	}

	live, stop := m.SubscribeOperations()
	defer stop()

	if _, err := m.Search(ctx, "battery", 5); err != nil {
		t.Fatalf("Search: %v", err)
	}
	select {
	case op := <-live:
		if op.Op != "read" || op.Detail != "search" {
			t.Errorf("live op = %+v", op)
		}
		if op.Actor != "MCP/test-client 1.0" {
			t.Errorf("actor = %q", op.Actor)
		}
		if op.Backend != SQLiteHybridID {
			t.Errorf("backend = %q", op.Backend)
		}
		if !op.OK || op.Count < 0 || op.Count > 5 {
			t.Errorf("search op = %+v", op)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no live operation for a real search")
	}

	ops := m.OperationsSince(latest)
	var sawRead, sawWrite bool
	for _, op := range ops {
		if op.Op == "read" && op.Detail == "search" {
			sawRead = true
		}
		if op.Op == "write" && op.Detail == "memory" {
			sawWrite = true
		}
	}
	if !sawRead {
		t.Errorf("OperationsSince missed the search: %+v", ops)
	}
	if !sawWrite {
		// The insert happened before `latest`, so ask the ring again.
		for _, op := range m.RecentOperations(0) {
			if op.Op == "write" && op.Detail == "memory" && op.Actor == "MCP/test-client 1.0" {
				sawWrite = true
			}
		}
	}
	if !sawWrite {
		t.Errorf("no write operation was recorded: %+v", m.RecentOperations(0))
	}

	// An unnamed caller is magpie itself, never an invented agent.
	if got := ActorOf(context.Background()); got != "magpie" {
		t.Errorf("default actor = %q", got)
	}
}

func TestEventRingIsBoundedAndKeepsOrder(t *testing.T) {
	e := newEventRing()
	for i := 0; i < eventRingSize+10; i++ {
		e.add(Operation{Backend: "sqlite_hybrid", Op: "read", Detail: "records"})
	}
	all := e.recent(0)
	if len(all) != eventRingSize {
		t.Fatalf("ring kept %d, want %d", len(all), eventRingSize)
	}
	for i := 1; i < len(all); i++ {
		if all[i].ID <= all[i-1].ID {
			t.Fatalf("ids out of order at %d: %d then %d", i, all[i-1].ID, all[i].ID)
		}
	}
	last := all[len(all)-1].ID
	if got := e.since(last); len(got) != 0 {
		t.Errorf("since(last) = %d operations, want 0", len(got))
	}
	if got := e.since(last - 2); len(got) != 2 {
		t.Errorf("since(last-2) = %d, want 2", len(got))
	}
}

func TestDocumentPanelSkipsUnreadableNotes(t *testing.T) {
	m := panelTestManager(t)
	dir := m.Vault().Dir()
	if err := os.WriteFile(filepath.Join(dir, "broken.md"), []byte("not a note\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	data := m.documentPanel()
	if !data.Available {
		t.Fatalf("documents unavailable: %s", data.Error)
	}
	if len(data.Notes) != 1 || data.Notes[0].File != "broken.md" {
		t.Fatalf("notes = %+v", data.Notes)
	}
	// It is listed as the real file it is. A note with no frontmatter takes
	// its id and topic from its own file name (vault.readNote's rule); the
	// fields the file does not carry stay empty rather than being guessed.
	if data.Notes[0].MemoryID != "broken" || data.Notes[0].Topic != "broken" {
		t.Errorf("note fields = %+v", data.Notes[0])
	}
	if data.Notes[0].Verdict != "" {
		t.Errorf("invented verdict: %+v", data.Notes[0])
	}
}

func TestDocumentPanelSaysUnavailableWhenTheVaultIsGone(t *testing.T) {
	m := panelTestManager(t)
	if err := os.RemoveAll(m.Vault().Dir()); err != nil {
		t.Fatal(err)
	}
	data := m.documentPanel()
	if data.Available {
		t.Fatalf("a removed vault reported available: %+v", data)
	}
	if data.Error == "" {
		t.Error("a removed vault reported no error")
	}
	if len(data.Notes) != 0 {
		t.Errorf("a removed vault listed %d notes", len(data.Notes))
	}
}
