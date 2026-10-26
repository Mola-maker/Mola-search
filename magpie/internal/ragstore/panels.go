package ragstore

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/yetone/magpie/internal/searchdet"
	"github.com/yetone/magpie/internal/usmf"
)

// This file is the read-only view behind the page's six database panels.
// Every field here is measured or read from a backend when the panel is
// asked for: record nodes and relation edges, stored vectors, the vault's
// real note files, the SQLite schema's own row counts and the lexical index's
// real configuration. A backend that cannot answer carries Available false
// and the error it returned; nothing is filled in on its behalf.

// PanelsData is the whole payload of GET /api/rag/panels.
type PanelsData struct {
	ActiveDBID  string                       `json:"activeDbId"`
	Backends    []searchdet.DBBackendInfo    `json:"backends"`
	Records     []usmf.CanonicalMemoryRecord `json:"records"`
	Graph       GraphPanel                   `json:"graph"`
	Vectors     VectorPanel                  `json:"vectors"`
	Documents   DocumentPanel                `json:"documents"`
	Relational  RelationalPanel              `json:"relational"`
	Lexical     LexicalPanel                 `json:"lexical"`
	GeneratedAt string                       `json:"generatedAt"`
}

// GraphNode is one stored fact as the topology draws it.
type GraphNode struct {
	ID        string `json:"id"`
	Topic     string `json:"topic"`
	Claim     string `json:"claim"`
	Verdict   string `json:"verdict"`
	Domain    string `json:"domain"`
	ValidFrom string `json:"validFrom"`
	InvalidAt string `json:"invalidAt"`
	Authority int    `json:"authority"`
}

// GraphEdge is one real relation edge: from memory_edges in SQLite, or from
// a record's own relationEdge spec when the active backend is the vault.
type GraphEdge struct {
	Src       string `json:"src"`
	Dst       string `json:"dst"`
	Type      string `json:"type"`
	ValidFrom string `json:"validFrom"`
	InvalidAt string `json:"invalidAt"`
}

// GraphPanel is the relation/temporal topology of the active corpus.
type GraphPanel struct {
	Available     bool        `json:"available"`
	Error         string      `json:"error,omitempty"`
	Nodes         []GraphNode `json:"nodes"`
	Edges         []GraphEdge `json:"edges"`
	ExternalEdges int         `json:"externalEdges"`
}

// VectorPoint is one stored embedding projected into three dimensions. The
// coordinates are a real projection of the vector's own values: the three
// dimensions with the largest measured variance, centred and scaled.
type VectorPoint struct {
	MemoryID string  `json:"memoryId"`
	Topic    string  `json:"topic"`
	Verdict  string  `json:"verdict"`
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
	Z        float64 `json:"z"`
	// Source is "stored" when the vector came from memory_vectors and
	// "computed" when the active backend keeps no vectors and this process
	// re-embedded the record with the live embedder.
	Source string `json:"source"`
}

// VectorAxis names one projected dimension and the variance that earned it.
type VectorAxis struct {
	Dim      int     `json:"dim"`
	Variance float64 `json:"variance"`
}

// VectorPanel is the embedding space as it really is.
type VectorPanel struct {
	Available bool          `json:"available"`
	Error     string        `json:"error,omitempty"`
	Points    []VectorPoint `json:"points"`
	Axes      []VectorAxis  `json:"axes"`
	Dims      int           `json:"dims"`
	Model     string        `json:"model"`
	// Models are the embedding models the projected vectors were really
	// produced by; a corpus migrated with a new embedder can hold more than
	// one, and the panel says so instead of naming only the live model.
	Models   []string `json:"models"`
	Stored   int      `json:"stored"`
	Computed int      `json:"computed"`
	Skipped  int      `json:"skipped"`
}

// DocumentNote is one real .md file in the vault folder.
type DocumentNote struct {
	File     string `json:"file"`
	MemoryID string `json:"memoryId"`
	Topic    string `json:"topic"`
	Verdict  string `json:"verdict"`
	Bytes    int64  `json:"bytes"`
	Modified string `json:"modified"`
}

// DocumentPanel is the vault as a document store: the files themselves.
type DocumentPanel struct {
	Available  bool           `json:"available"`
	Error      string         `json:"error,omitempty"`
	Dir        string         `json:"dir"`
	Notes      []DocumentNote `json:"notes"`
	TotalBytes int64          `json:"totalBytes"`
}

// TableInfo is one SQLite table and its real shape.
type TableInfo struct {
	Name    string   `json:"name"`
	Rows    int      `json:"rows"`
	Columns []string `json:"columns"`
}

// VerdictCount is how many records carry one verdict, as stored.
type VerdictCount struct {
	Verdict string `json:"verdict"`
	Count   int    `json:"count"`
}

// RelationalPanel reads the primary store's schema and validity windows.
type RelationalPanel struct {
	Available bool           `json:"available"`
	Error     string         `json:"error,omitempty"`
	Tables    []TableInfo    `json:"tables"`
	ByVerdict []VerdictCount `json:"verdicts"`
	Current   int            `json:"current"`
	Expired   int            `json:"expired"`
	Future    int            `json:"future"`
}

// LexicalPanel is the full-text/search backend's own configuration.
type LexicalPanel struct {
	Available      bool   `json:"available"`
	Error          string `json:"error,omitempty"`
	FTS5           bool   `json:"fts5"`
	Records        int    `json:"records"`
	Vectors        int    `json:"vectors"`
	Edges          int    `json:"edges"`
	Habits         int    `json:"habits"`
	EmbeddingModel string `json:"embeddingModel"`
	Dims           int    `json:"dims"`
}

// Panels assembles every panel from the backends this manager owns. The
// record list is the active backend's, the edges are the primary mirror's
// plus each record's own relation spec, and the schema counts are SQLite's.
func (m *Manager) Panels(ctx context.Context) (PanelsData, error) {
	out := PanelsData{
		ActiveDBID:  m.ActiveID(),
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
	}
	recs, err := m.Records(ctx)
	if err != nil {
		return out, err
	}
	if recs == nil {
		recs = []usmf.CanonicalMemoryRecord{}
	}
	out.Records = recs
	if backends, err := m.Status(ctx); err == nil {
		out.Backends = backends
	}
	out.Graph = m.graphPanel(ctx, recs)
	out.Vectors = m.vectorPanel(ctx, recs)
	out.Documents = m.documentPanel()
	out.Relational = m.relationalPanel(ctx)
	out.Lexical = m.lexicalPanel(ctx)
	return out, nil
}

// Records reads the active backend's whole corpus and records the read.
func (m *Manager) Records(ctx context.Context) ([]usmf.CanonicalMemoryRecord, error) {
	active := m.Active()
	if active == nil {
		return nil, fmt.Errorf("ragstore: no active backend")
	}
	backend := active.ID()
	started := time.Now()
	recs, err := active.All(ctx)
	m.note(ctx, backend, "read", "records", len(recs), err == nil, time.Since(started))
	return recs, err
}

func (m *Manager) graphPanel(ctx context.Context, recs []usmf.CanonicalMemoryRecord) GraphPanel {
	p := GraphPanel{Available: true, Nodes: make([]GraphNode, 0, len(recs)), Edges: []GraphEdge{}}
	known := make(map[string]bool, len(recs))
	for _, r := range recs {
		known[r.MemoryID] = true
		p.Nodes = append(p.Nodes, GraphNode{
			ID: r.MemoryID, Topic: r.Topic, Claim: r.Claim, Verdict: r.Verdict,
			Domain: r.SourceDomain, ValidFrom: r.ValidFrom, InvalidAt: r.InvalidAt,
			Authority: r.AuthorityLevel,
		})
	}
	type edgeKey struct{ src, dst, typ string }
	seen := make(map[edgeKey]bool)
	add := func(e GraphEdge) {
		k := edgeKey{e.Src, e.Dst, e.Type}
		if e.Src == "" || e.Dst == "" || seen[k] {
			return
		}
		seen[k] = true
		p.Edges = append(p.Edges, e)
	}
	for _, r := range recs {
		for _, spec := range parseRelationEdges(r.RelationEdge) {
			add(GraphEdge{Src: r.MemoryID, Dst: spec.dst, Type: spec.typ, ValidFrom: r.ValidFrom, InvalidAt: r.InvalidAt})
		}
	}
	if m.primary != nil {
		edges, err := m.primary.allEdges(ctx)
		if err != nil {
			p.Available = false
			p.Error = err.Error()
			return p
		}
		for _, e := range edges {
			add(e)
		}
	}
	for _, e := range p.Edges {
		if !known[e.Src] || !known[e.Dst] {
			p.ExternalEdges++
		}
	}
	return p
}

func (m *Manager) vectorPanel(ctx context.Context, recs []usmf.CanonicalMemoryRecord) VectorPanel {
	p := VectorPanel{Available: true, Points: []VectorPoint{}, Axes: []VectorAxis{}, Models: []string{}, Model: m.EmbeddingModel(), Dims: m.emb.Dim()}
	stored := map[string]Vector{}
	if m.primary != nil {
		vectors, err := m.primary.allVectors(ctx)
		if err != nil {
			p.Available = false
			p.Error = err.Error()
			return p
		}
		stored = vectors
	}

	type row struct {
		rec    usmf.CanonicalMemoryRecord
		vec    []float32
		source string
		model  string
	}
	var rows []row
	for _, r := range recs {
		vec := []float32(nil)
		source := ""
		model := ""
		if sv, ok := stored[r.MemoryID]; ok && len(sv.Vec) > 0 {
			vec = sv.Vec
			source = "stored"
			model = sv.Model
			p.Stored++
		} else {
			text := documentText(fieldsOf(r.Topic, r.Claim, r.EvidenceText))
			computed, err := m.emb.Embed(ctx, text)
			if err != nil {
				continue
			}
			vec = computed
			source = "computed"
			model = m.emb.ID()
			p.Computed++
		}
		if len(vec) == 0 {
			continue
		}
		rows = append(rows, row{rec: r, vec: vec, source: source, model: model})
	}
	if len(rows) < 2 {
		p.Available = false
		p.Error = "fewer than two vectors"
		return p
	}

	// Vectors of one width can be projected together. A mixed corpus keeps
	// the width of its first vector and counts the rest as skipped.
	dims := len(rows[0].vec)
	used := make([]row, 0, len(rows))
	for _, r := range rows {
		if len(r.vec) == dims {
			used = append(used, r)
		} else {
			p.Skipped++
		}
	}
	if len(used) < 2 {
		p.Available = false
		p.Error = "fewer than two vectors of one width"
		return p
	}
	p.Dims = dims
	modelSet := map[string]bool{}
	for _, r := range used {
		if r.model != "" {
			modelSet[r.model] = true
		}
	}
	for model := range modelSet {
		p.Models = append(p.Models, model)
	}
	sort.Strings(p.Models)

	mean := make([]float64, dims)
	for _, r := range used {
		for d, v := range r.vec {
			mean[d] += float64(v)
		}
	}
	n := float64(len(used))
	for d := range mean {
		mean[d] /= n
	}
	variance := make([]float64, dims)
	for _, r := range used {
		for d, v := range r.vec {
			delta := float64(v) - mean[d]
			variance[d] += delta * delta
		}
	}
	for d := range variance {
		variance[d] /= n
	}
	axes := make([]VectorAxis, 0, dims)
	for d, v := range variance {
		axes = append(axes, VectorAxis{Dim: d, Variance: v})
	}
	sort.Slice(axes, func(i, j int) bool {
		if axes[i].Variance != axes[j].Variance {
			return axes[i].Variance > axes[j].Variance
		}
		return axes[i].Dim < axes[j].Dim
	})
	axes = axes[:3]
	for _, a := range axes {
		if a.Variance <= 1e-12 {
			p.Available = false
			p.Error = "the vectors do not spread across three dimensions"
			return p
		}
	}
	p.Axes = append(p.Axes, axes...)

	for _, r := range used {
		coords := [3]float64{}
		for a, axis := range axes {
			z := (float64(r.vec[axis.Dim]) - mean[axis.Dim]) / math.Sqrt(axis.Variance)
			coords[a] = math.Tanh(z / 2)
		}
		p.Points = append(p.Points, VectorPoint{
			MemoryID: r.rec.MemoryID, Topic: r.rec.Topic, Verdict: r.rec.Verdict,
			X: round6(coords[0]), Y: round6(coords[1]), Z: round6(coords[2]),
			Source: r.source,
		})
	}
	return p
}

func (m *Manager) documentPanel() DocumentPanel {
	p := DocumentPanel{Available: true, Notes: []DocumentNote{}}
	if m.vault == nil {
		p.Available = false
		p.Error = "no markdown vault"
		return p
	}
	p.Dir = m.vault.Dir()
	entries, err := os.ReadDir(p.Dir)
	if err != nil {
		p.Available = false
		p.Error = err.Error()
		return p
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".md") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		note := DocumentNote{
			File:     entry.Name(),
			Bytes:    info.Size(),
			Modified: info.ModTime().UTC().Format(time.RFC3339),
		}
		if rec, err := m.vault.readNote(filepath.Join(p.Dir, entry.Name())); err == nil {
			note.MemoryID = rec.MemoryID
			note.Topic = rec.Topic
			note.Verdict = rec.Verdict
		}
		p.Notes = append(p.Notes, note)
		p.TotalBytes += info.Size()
	}
	sort.SliceStable(p.Notes, func(i, j int) bool {
		if p.Notes[i].Modified != p.Notes[j].Modified {
			return p.Notes[i].Modified > p.Notes[j].Modified
		}
		return p.Notes[i].File < p.Notes[j].File
	})
	return p
}

func (m *Manager) relationalPanel(ctx context.Context) RelationalPanel {
	p := RelationalPanel{Available: true, Tables: []TableInfo{}, ByVerdict: []VerdictCount{}}
	if m.primary == nil {
		p.Available = false
		p.Error = "no primary store"
		return p
	}
	for _, name := range []string{"memory_records", "memory_vectors", "memory_edges", "agent_habits"} {
		rows, err := m.primary.tableRows(ctx, name)
		if err != nil {
			p.Available = false
			p.Error = err.Error()
			return p
		}
		columns, err := m.primary.tableColumns(ctx, name)
		if err != nil {
			p.Available = false
			p.Error = err.Error()
			return p
		}
		p.Tables = append(p.Tables, TableInfo{Name: name, Rows: rows, Columns: columns})
	}
	verdicts, err := m.primary.verdictCounts(ctx)
	if err != nil {
		p.Available = false
		p.Error = err.Error()
		return p
	}
	p.ByVerdict = verdicts
	now := time.Now().UTC().Format(time.RFC3339)
	current, expired, future, err := m.primary.validityCounts(ctx, now)
	if err != nil {
		p.Available = false
		p.Error = err.Error()
		return p
	}
	p.Current, p.Expired, p.Future = current, expired, future
	return p
}

func (m *Manager) lexicalPanel(ctx context.Context) LexicalPanel {
	p := LexicalPanel{Available: true, EmbeddingModel: m.EmbeddingModel(), Dims: m.emb.Dim()}
	if m.primary == nil {
		p.Available = false
		p.Error = "no primary store"
		return p
	}
	p.FTS5 = m.primary.UsesFTS5()
	for name, dst := range map[string]*int{
		"memory_records": &p.Records,
		"memory_vectors": &p.Vectors,
		"memory_edges":   &p.Edges,
		"agent_habits":   &p.Habits,
	} {
		n, err := m.primary.tableRows(ctx, name)
		if err != nil {
			p.Available = false
			p.Error = err.Error()
			return p
		}
		*dst = n
	}
	return p
}

// allEdges reads every relation edge the primary mirror holds.
func (s *SQLiteHybridStore) allEdges(ctx context.Context) ([]GraphEdge, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT src, dst, edge_type, valid_from, invalid_at
FROM memory_edges ORDER BY src, dst, edge_type`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []GraphEdge{}
	for rows.Next() {
		var e GraphEdge
		if err := rows.Scan(&e.Src, &e.Dst, &e.Type, &e.ValidFrom, &e.InvalidAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// allVectors reads every stored embedding, keyed by memory id, with the
// model that really produced it.
func (s *SQLiteHybridStore) allVectors(ctx context.Context) (map[string]Vector, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT memory_id, vec, model, updated_at FROM memory_vectors`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Vector{}
	for rows.Next() {
		var v Vector
		var blob []byte
		if err := rows.Scan(&v.MemoryID, &blob, &v.Model, &v.UpdatedAt); err != nil {
			return nil, err
		}
		if v.Vec = decodeVector(blob); len(v.Vec) > 0 {
			out[v.MemoryID] = v
		}
	}
	return out, rows.Err()
}

// tableRows counts one of the primary store's own tables. name is one of the
// fixed names this file passes, never user input.
func (s *SQLiteHybridStore) tableRows(ctx context.Context, name string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM %q`, name)).Scan(&n)
	return n, err
}

// tableColumns reads a table's declared columns in order.
func (s *SQLiteHybridStore) tableColumns(ctx context.Context, name string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`PRAGMA table_info(%q)`, name))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var cid, notNull, pk int
		var col, typ string
		var dflt any
		if err := rows.Scan(&cid, &col, &typ, &notNull, &dflt, &pk); err != nil {
			return nil, err
		}
		out = append(out, col)
	}
	return out, rows.Err()
}

// verdictCounts groups the corpus by the verdict it really carries.
func (s *SQLiteHybridStore) verdictCounts(ctx context.Context) ([]VerdictCount, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT verdict, COUNT(*) FROM memory_records GROUP BY verdict ORDER BY COUNT(*) DESC, verdict`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []VerdictCount{}
	for rows.Next() {
		var v VerdictCount
		if err := rows.Scan(&v.Verdict, &v.Count); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// validityCounts splits the corpus by its real bi-temporal window: a record
// is current, expired, or not yet valid. now is the caller's UTC RFC3339.
// datetime() parses the stored offsets, so a record written in +08:00 is
// compared by its instant, not by its text.
func (s *SQLiteHybridStore) validityCounts(ctx context.Context, now string) (current, expired, future int, err error) {
	err = s.db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM memory_records
WHERE invalid_at <> '' AND datetime(invalid_at) <= datetime(?)`, now).Scan(&expired)
	if err != nil {
		return 0, 0, 0, err
	}
	err = s.db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM memory_records
WHERE valid_from <> '' AND datetime(valid_from) > datetime(?)
  AND (invalid_at = '' OR datetime(invalid_at) > datetime(?))`, now, now).Scan(&future)
	if err != nil {
		return 0, 0, 0, err
	}
	err = s.db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM memory_records
WHERE (invalid_at = '' OR datetime(invalid_at) > datetime(?))
  AND (valid_from = '' OR datetime(valid_from) <= datetime(?))`, now, now).Scan(&current)
	if err != nil {
		return 0, 0, 0, err
	}
	return current, expired, future, nil
}

func round6(v float64) float64 {
	return math.Round(v*1e6) / 1e6
}
