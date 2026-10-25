package ragstore

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/yetone/magpie/internal/usmf"
)

// SwapReport is the record of one hot swap: what moved, how long each phase
// took, the hashes that had to agree before the switch, and where the
// pre-switch backup was written. Every field is measured during the swap.
type SwapReport struct {
	From           string   `json:"from"`
	To             string   `json:"to"`
	Records        int      `json:"records"`
	EmbeddingModel string   `json:"embeddingModel"`
	SHA256         string   `json:"sha256"`
	Phase1WriteMs  int64    `json:"phase1WriteMs"`
	Phase2EmbedMs  int64    `json:"phase2EmbedMs"`
	Phase3SwitchMs int64    `json:"phase3SwitchMs"`
	TotalMs        int64    `json:"totalMs"`
	BackupPath     string   `json:"backupPath"`
	Log            []string `json:"log"`
}

// HotSwap migrates the whole corpus from the active backend to targetID in
// three phases and switches only after the migrated copy proves it is the
// same corpus:
//
//  1. Read every record from the current backend and write it to the target
//     (the double-write phase: the source is still serving reads, so nothing
//     is unavailable while this runs).
//  2. Recompute every record's vector with the given embedder and write it
//     into the target, so the new corpus is embedded with one model instead
//     of inheriting a mix.
//  3. Hash both copies (memory_id|claim|evidence, sorted by id), compare, and
//     only then flip the active pointer. A mismatch leaves the old backend
//     active and returns an error; the target keeps what it received and can
//     be retried.
//
// Before phase 1 the current corpus is exported to a USMF backup beside the
// data directory, so even a failed swap has a readable copy of what was
// there.
func (m *Manager) HotSwap(ctx context.Context, targetID string, embedder Embedder) (report *SwapReport, err error) {
	// The page names the remote backends "milvus_hybrid" and
	// "elasticsearch_hybrid"; the registry keys them by their engine ids.
	// Accept either spelling so the button works whichever name a caller
	// sends.
	targetID = canonicalBackendID(targetID)
	m.mu.RLock()
	source := m.backends[m.activeID]
	sourceID := m.activeID
	target, ok := m.backends[targetID]
	m.mu.RUnlock()
	if source == nil {
		return nil, errors.New("ragstore: no active backend")
	}
	if !ok {
		return nil, fmt.Errorf("ragstore: unknown backend %q", targetID)
	}
	if sourceID == targetID {
		return nil, fmt.Errorf("ragstore: %s is already active", targetID)
	}
	if embedder == nil {
		embedder = m.emb
	}

	report = &SwapReport{From: sourceID, To: targetID, EmbeddingModel: embedder.ID()}
	started := time.Now()
	defer func() {
		count := 0
		if report != nil {
			count = report.Records
		}
		m.note(ctx, targetID, "write", "swap", count, err == nil, time.Since(started))
	}()

	// Backup first: the snapshot must describe the corpus as it was before
	// anything was written to the target.
	recs, err := source.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("ragstore: read %s: %w", sourceID, err)
	}
	report.Records = len(recs)
	backupPath := filepath.Join(backupDir(),
		fmt.Sprintf("backup_%s_%s.magpie-rag.jsonl", sourceID, started.UTC().Format("20060102-150405")))
	if err := writeRecordsJSONL(backupPath, recs); err != nil {
		return nil, fmt.Errorf("ragstore: backup: %w", err)
	}
	if err := verifyBackup(backupPath, corpusHash(recs)); err != nil {
		return nil, fmt.Errorf("ragstore: backup unreadable: %w", err)
	}
	report.BackupPath = backupPath
	report.Log = append(report.Log, fmt.Sprintf("阶段0 备份：%d 条记录已写入并校验 %s", len(recs), backupPath))

	// Phase 1: write every record to the target.
	phaseStart := time.Now()
	if setter, ok := target.(embedderSetter); ok {
		setter.SetEmbedder(embedder)
	}
	if err := target.Upsert(ctx, recs); err != nil {
		return nil, fmt.Errorf("ragstore: phase 1 write %s: %w", targetID, err)
	}
	report.Phase1WriteMs = time.Since(phaseStart).Milliseconds()
	report.Log = append(report.Log,
		fmt.Sprintf("阶段1 双写：%d 条记录写入 %s，耗时 %d ms", len(recs), targetID, report.Phase1WriteMs))

	// Phase 2: re-embed every record with the target's model and rewrite.
	phaseStart = time.Now()
	reembedded := make([]usmf.CanonicalMemoryRecord, len(recs))
	copy(reembedded, recs)
	texts := make([]string, len(reembedded))
	for i, r := range reembedded {
		texts[i] = documentText(fieldsOf(r.Topic, r.Claim, r.EvidenceText))
		reembedded[i].EmbeddingModel = embedder.ID()
	}
	if _, err := embedder.EmbedBatch(ctx, texts); err != nil {
		return nil, fmt.Errorf("ragstore: phase 2 embed: %w", err)
	}
	if err := target.Upsert(ctx, reembedded); err != nil {
		return nil, fmt.Errorf("ragstore: phase 2 write %s: %w", targetID, err)
	}
	report.Phase2EmbedMs = time.Since(phaseStart).Milliseconds()
	report.Log = append(report.Log,
		fmt.Sprintf("阶段2 重嵌入：%d 条记录用 %s 重算向量，耗时 %d ms", len(reembedded), embedder.ID(), report.Phase2EmbedMs))

	// Phase 3: verify the two corpora are the same, then flip.
	phaseStart = time.Now()
	targetRecs, err := target.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("ragstore: read back %s: %w", targetID, err)
	}
	sourceHash := corpusHash(recs)
	targetHash := corpusHash(targetRecs)
	if sourceHash != targetHash {
		return nil, fmt.Errorf("ragstore: hot swap aborted: %s hash %s != %s hash %s; %s stays active",
			sourceID, sourceHash, targetID, targetHash, sourceID)
	}
	m.setActive(targetID)
	report.SHA256 = targetHash
	report.Phase3SwitchMs = time.Since(phaseStart).Milliseconds()
	report.TotalMs = time.Since(started).Milliseconds()
	report.Log = append(report.Log,
		fmt.Sprintf("阶段3 切换：SHA-256 %s 一致，主库切到 %s，耗时 %d ms；总计 %d ms",
			shortHash(targetHash), targetID, report.Phase3SwitchMs, report.TotalMs))
	return report, nil
}

// canonicalBackendID maps the aliases a caller may send onto the id the
// registry uses. An id that isn't an alias is returned unchanged, so a real
// backend id and an unknown one both behave as before.
func canonicalBackendID(id string) string {
	switch strings.ToLower(strings.TrimSpace(id)) {
	case "milvus_hybrid":
		return MilvusID
	case "elasticsearch_hybrid":
		return ElasticsearchID
	case "postgres_hybrid":
		return PostgresID
	case "sqlite":
		return SQLiteHybridID
	case "vault":
		return MarkdownVaultID
	}
	return id
}

// corpusHash is the SHA-256 of "memoryID|claim|evidence" for every record in
// memory-id order. Ordering makes it independent of how a backend happened
// to store the rows; the three fields are what a search actually answers
// with, so two corpora that agree on them are the same corpus.
func corpusHash(recs []usmf.CanonicalMemoryRecord) string {
	sorted := make([]usmf.CanonicalMemoryRecord, len(recs))
	copy(sorted, recs)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].MemoryID < sorted[j].MemoryID })
	h := sha256.New()
	for _, r := range sorted {
		io.WriteString(h, r.MemoryID)
		h.Write([]byte{'|'})
		io.WriteString(h, r.Claim)
		h.Write([]byte{'|'})
		io.WriteString(h, r.EvidenceText)
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func shortHash(h string) string {
	if len(h) > 16 {
		return h[:16]
	}
	return h
}

// ExportUSMF writes the active corpus as JSON Lines — one
// CanonicalMemoryRecord per line, which is the USMF interchange form — and
// returns the number of records written. It streams, so a large corpus isn't
// held twice in memory.
func (m *Manager) ExportUSMF(ctx context.Context, w io.Writer) (n int, err error) {
	active := m.Active()
	backend := ""
	if active != nil {
		backend = active.ID()
	}
	started := time.Now()
	defer func() {
		m.note(ctx, backend, "read", "export", n, err == nil, time.Since(started))
	}()
	if active == nil {
		return 0, fmt.Errorf("ragstore: no active backend")
	}
	recs, err := active.All(ctx)
	if err != nil {
		return 0, err
	}
	sort.Slice(recs, func(i, j int) bool { return recs[i].MemoryID < recs[j].MemoryID })
	bw := bufio.NewWriter(w)
	enc := json.NewEncoder(bw)
	for _, r := range recs {
		if err := enc.Encode(r); err != nil {
			return n, err
		}
		n++
	}
	if err := bw.Flush(); err != nil {
		return n, err
	}
	return n, nil
}

// ImportUSMF reads JSON Lines and writes every parseable record to the
// active backend, returning how many were imported. A malformed line is
// skipped and counted, not fatal: an export from an older build with one
// broken line should still restore the rest of the corpus.
func (m *Manager) ImportUSMF(ctx context.Context, r io.Reader) (imported int, err error) {
	active := m.Active()
	backend := ""
	if active != nil {
		backend = active.ID()
	}
	started := time.Now()
	defer func() {
		m.note(ctx, backend, "write", "import", imported, err == nil, time.Since(started))
	}()
	sc := bufio.NewScanner(r)
	// A record with a long evidence text exceeds bufio's 64KB default line.
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	var batch []usmf.CanonicalMemoryRecord
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if err := m.Active().Upsert(ctx, batch); err != nil {
			return err
		}
		imported += len(batch)
		batch = batch[:0]
		return nil
	}
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var rec usmf.CanonicalMemoryRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil || strings.TrimSpace(rec.MemoryID) == "" {
			continue
		}
		batch = append(batch, rec)
		if len(batch) >= 100 {
			if err := flush(); err != nil {
				return imported, err
			}
		}
	}
	if err := sc.Err(); err != nil {
		return imported, err
	}
	if err := flush(); err != nil {
		return imported, err
	}
	// Unreadable lines are skipped by design (an export from an older build
	// may carry one); the caller gets the number that made it, not a failure
	// that would suggest nothing was imported.
	return imported, nil
}

// writeRecordsJSONL writes the corpus to path in USMF form, through a temp
// file and a rename.
func writeRecordsJSONL(path string, recs []usmf.CanonicalMemoryRecord) error {
	sorted := make([]usmf.CanonicalMemoryRecord, len(recs))
	copy(sorted, recs)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].MemoryID < sorted[j].MemoryID })
	lines := make([]string, 0, len(sorted))
	for _, r := range sorted {
		line, err := marshalRecord(r)
		if err != nil {
			return err
		}
		lines = append(lines, line)
	}
	return writeJSONL(path, lines)
}

// readJSONLRecords reads a USMF file back, for a caller verifying a backup.
func readJSONLRecords(r io.Reader) ([]usmf.CanonicalMemoryRecord, int, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	var out []usmf.CanonicalMemoryRecord
	skipped := 0
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var rec usmf.CanonicalMemoryRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			skipped++
			continue
		}
		out = append(out, rec)
	}
	return out, skipped, sc.Err()
}

// verifyBackup re-reads a written backup and confirms its hash matches the
// corpus that produced it.
func verifyBackup(path string, want string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	recs, _, err := readJSONLRecords(f)
	if err != nil {
		return err
	}
	if got := corpusHash(recs); got != want {
		return fmt.Errorf("ragstore: backup %s hashes to %s, want %s", path, shortHash(got), shortHash(want))
	}
	return nil
}
