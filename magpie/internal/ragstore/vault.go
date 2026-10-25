package ragstore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yetone/magpie/internal/searchdet"
	"github.com/yetone/magpie/internal/usmf"
)

// MarkdownVaultStore keeps the corpus as one Markdown file per record in a
// directory the user can open, read, edit and sync with any notes app. It is
// the backend that survives a reinstall of the app: the facts are plain text
// with a YAML frontmatter header, and every field the SQLite store keeps is
// in that header.
//
// Files are written through a temporary file and a rename, because a notes
// app, a sync client or a second MolaSearch window may be reading the
// directory at the same moment and must never see a half-written file.
type MarkdownVaultStore struct {
	id  string
	dir string

	mu  sync.RWMutex
	emb Embedder
}

// MarkdownVaultID is the backend id Status and HotSwap use for the vault.
const MarkdownVaultID = "markdown_vault"

// DefaultVaultDir is where the vault lives unless a caller names a folder.
func DefaultVaultDir() string {
	return filepath.Join(searchdet.DataDir(), "vault")
}

// OpenVault opens (creating if needed) the vault directory.
func OpenVault(dir string) (*MarkdownVaultStore, error) {
	return openVaultID(dir, MarkdownVaultID)
}

func openVaultID(dir, id string) (*MarkdownVaultStore, error) {
	if dir == "" {
		dir = DefaultVaultDir()
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &MarkdownVaultStore{id: id, dir: dir, emb: &LocalHashEmbedder{Dims: DefaultDims}}, nil
}

// ID implements Store.
func (s *MarkdownVaultStore) ID() string { return s.id }

// Dir is the folder this vault owns, for Status's DSN column.
func (s *MarkdownVaultStore) Dir() string { return s.dir }

// SetEmbedder implements embedderSetter.
func (s *MarkdownVaultStore) SetEmbedder(e Embedder) {
	if e == nil {
		return
	}
	s.mu.Lock()
	s.emb = e
	s.mu.Unlock()
}

func (s *MarkdownVaultStore) embedder() Embedder {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.emb == nil {
		return &LocalHashEmbedder{Dims: DefaultDims}
	}
	return s.emb
}

// Ping implements Store: the vault is reachable when its directory is.
func (s *MarkdownVaultStore) Ping(_ context.Context) error {
	st, err := os.Stat(s.dir)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return fmt.Errorf("ragstore: vault %s is not a directory", s.dir)
	}
	return nil
}

// Count reports how many note files the vault holds.
func (s *MarkdownVaultStore) Count(ctx context.Context) (int, error) {
	recs, err := s.All(ctx)
	return len(recs), err
}

// All implements Store by reading every note in the directory. A file that
// doesn't parse is skipped rather than aborting the scan: one broken note
// must not hide the rest of the vault.
func (s *MarkdownVaultStore) All(_ context.Context) ([]usmf.CanonicalMemoryRecord, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	var out []usmf.CanonicalMemoryRecord
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
			continue
		}
		rec, err := s.readNote(filepath.Join(s.dir, e.Name()))
		if err != nil {
			continue
		}
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].MemoryID < out[j].MemoryID })
	return out, nil
}

// Upsert implements Store: one file per record, rewritten whole.
func (s *MarkdownVaultStore) Upsert(ctx context.Context, recs []usmf.CanonicalMemoryRecord) error {
	if len(recs) == 0 {
		return nil
	}
	emb := s.embedder()
	texts := make([]string, len(recs))
	for i, r := range recs {
		texts[i] = documentText(fieldsOf(r.Topic, r.Claim, r.EvidenceText))
	}
	if _, err := emb.EmbedBatch(ctx, texts); err != nil {
		return err
	}
	for _, r := range recs {
		if strings.TrimSpace(r.MemoryID) == "" {
			return errors.New("ragstore: record with empty memoryId")
		}
		if r.EmbeddingModel == "" {
			r.EmbeddingModel = emb.ID()
		}
		if err := s.writeNote(r); err != nil {
			return err
		}
	}
	return nil
}

// Delete removes one note. It reports whether a file was there.
func (s *MarkdownVaultStore) Delete(_ context.Context, memoryID string) (bool, error) {
	err := os.Remove(s.notePath(memoryID))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// Search implements Store with the same hybrid shape as the SQLite store:
// cosine over embeddings computed from the note text, BM25 over the same
// text, and RRF fusion. The vectors are not cached on disk — a vault is the
// human-readable copy, and its text is the source of truth, so recomputing
// keeps an edited note searchable without a reindex step.
func (s *MarkdownVaultStore) Search(ctx context.Context, query string, qvec []float32, k int) ([]Hit, error) {
	k = topK(k)
	recs, err := s.All(ctx)
	if err != nil {
		return nil, err
	}
	if len(recs) == 0 {
		return nil, nil
	}
	emb := s.embedder()
	if len(qvec) == 0 {
		qvec, err = emb.Embed(ctx, query)
		if err != nil {
			return nil, err
		}
	}
	now := time.Now().UTC()
	var docs []lexicalDoc
	cosine := make(map[string]float64, len(recs))
	var dense []scoredID
	byID := make(map[string]usmf.CanonicalMemoryRecord, len(recs))
	for _, r := range recs {
		if !recordValid(r, now) {
			continue
		}
		text := documentText(fieldsOf(r.Topic, r.Claim, r.EvidenceText))
		vec, err := emb.Embed(ctx, text)
		if err != nil {
			return nil, err
		}
		c := CosineSimilarity(qvec, vec)
		cosine[r.MemoryID] = c
		dense = append(dense, scoredID{r.MemoryID, c})
		docs = append(docs, makeLexicalDoc(r.MemoryID, text))
		byID[r.MemoryID] = r
	}
	if len(docs) == 0 {
		return nil, nil
	}
	sort.SliceStable(dense, func(i, j int) bool {
		if dense[i].score != dense[j].score {
			return dense[i].score > dense[j].score
		}
		return dense[i].id < dense[j].id
	})
	denseIDs := idsOf(dense)

	ix := newLexicalIndex(docs)
	bm25IDs := ix.rankDocuments(query)
	terms := Tokenize(query)
	bm25 := make(map[string]float64, len(bm25IDs))
	for _, d := range docs {
		if score := ix.score(terms, d); score > 0 {
			bm25[d.id] = score
		}
	}

	fused := fuseRanks(denseIDs, bm25IDs)
	ranked := make([]scoredID, 0, len(fused))
	for id, f := range fused {
		ranked = append(ranked, scoredID{id, f})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].id < ranked[j].id
	})
	if len(ranked) > k {
		ranked = ranked[:k]
	}
	hits := make([]Hit, 0, len(ranked))
	for _, s := range ranked {
		hits = append(hits, Hit{Record: byID[s.id], Cosine: cosine[s.id], BM25: bm25[s.id], RRF: s.score})
	}
	return hits, nil
}

// Close implements Store. The vault holds no open handle between calls, so
// there is nothing to release.
func (s *MarkdownVaultStore) Close() error { return nil }

// notePath is the file one record lives in. The id is percent-escaped so a
// record id that came from a URL can't escape the directory or collide with
// a path separator on Windows.
func (s *MarkdownVaultStore) notePath(memoryID string) string {
	return filepath.Join(s.dir, safeFileName(memoryID)+".md")
}

func safeFileName(id string) string {
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		case r < 128:
			fmt.Fprintf(&b, "%%%02X", r)
		default:
			fmt.Fprintf(&b, "%%u%04X", r)
		}
	}
	out := b.String()
	// Windows reserves these stems and hides a leading dot; a note named
	// CON.md can't be created at all.
	if out == "" {
		out = "record"
	}
	switch strings.ToUpper(out) {
	case "CON", "PRN", "AUX", "NUL", "COM1", "COM2", "LPT1", "LPT2":
		out = "_" + out
	}
	if strings.HasPrefix(out, ".") {
		out = "_" + out
	}
	return out
}

// writeNote renders one record and writes it through a temp file and a
// rename, the same way magpie writes every file another process may read.
func (s *MarkdownVaultStore) writeNote(r usmf.CanonicalMemoryRecord) error {
	body := renderNote(r)
	dir := s.dir
	tmp, err := os.CreateTemp(dir, ".note-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.WriteString(body); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, s.notePath(r.MemoryID)); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// renderNote is the note's text: frontmatter with every stored field, then
// the human-readable body. Values are YAML-quoted when they contain a
// character that would break the "key: value" line.
func renderNote(r usmf.CanonicalMemoryRecord) string {
	var b strings.Builder
	b.WriteString("---\n")
	writeFM := func(key, val string) {
		b.WriteString(key)
		b.WriteString(": ")
		b.WriteString(yamlScalar(val))
		b.WriteByte('\n')
	}
	writeFM("memory_id", r.MemoryID)
	writeFM("topic", r.Topic)
	writeFM("verdict", r.Verdict)
	writeFM("relation_edge", r.RelationEdge)
	writeFM("valid_from", r.ValidFrom)
	writeFM("invalid_at", r.InvalidAt)
	writeFM("source_url", r.SourceURL)
	writeFM("source_domain", r.SourceDomain)
	writeFM("authority_level", strconv.Itoa(r.AuthorityLevel))
	writeFM("embedding_model", r.EmbeddingModel)
	// The body's bullets are the human-readable copy, but a value that spans
	// lines or has meaningful edge whitespace can't be reconstructed from
	// them exactly. Those values also go into the header, quoted, so a read
	// back is byte-identical and a hot-swap hash still matches.
	if needsCarrier(r.Claim) {
		writeFM("claim", r.Claim)
	}
	if needsCarrier(r.EvidenceText) {
		writeFM("evidence_text", r.EvidenceText)
	}
	b.WriteString("---\n\n")
	if r.Topic != "" {
		b.WriteString("# ")
		b.WriteString(strings.TrimSpace(r.Topic))
		b.WriteString("\n\n")
	}
	b.WriteString("- **Claim**: ")
	b.WriteString(indentContinuation(r.Claim))
	b.WriteString("\n")
	if r.EvidenceText != "" {
		b.WriteString("- **Verified Evidence**: ")
		b.WriteString(indentContinuation(r.EvidenceText))
		b.WriteString("\n")
	}
	if r.Verdict != "" {
		b.WriteString("- **Verdict**: ")
		b.WriteString(strings.TrimSpace(r.Verdict))
		b.WriteString("\n")
	}
	if r.SourceURL != "" {
		b.WriteString("- **Source**: ")
		b.WriteString(strings.TrimSpace(r.SourceURL))
		b.WriteString("\n")
	}
	return b.String()
}

// indentContinuation keeps the first line of a value flush with its bullet
// and indents every following line by two spaces. That is what lets
// parseNoteBody tell a wrapped claim from the next field, and it is also how
// Markdown wants a continuation of a list item written.
func indentContinuation(s string) string {
	lines := strings.Split(s, "\n")
	for i := 1; i < len(lines); i++ {
		lines[i] = "  " + lines[i]
	}
	return strings.Join(lines, "\n")
}

// needsCarrier says whether a value must also be written into the
// frontmatter to survive a read back exactly. A single line with no
// significant edge whitespace round-trips through the body bullet; anything
// else needs the quoted header copy.
func needsCarrier(s string) bool {
	return strings.ContainsAny(s, "\r\n") || s != strings.TrimSpace(s)
}

// yamlScalar quotes a value only when leaving it bare would change how the
// line parses. Plain text stays plain, so a human editing the note sees the
// value as they wrote it.
func yamlScalar(v string) string {
	if v == "" {
		return `""`
	}
	needsQuote := strings.HasPrefix(v, " ") || strings.HasSuffix(v, " ") ||
		strings.ContainsAny(v, ":#\n\r\t\"'") ||
		v == "~" || v == "null" || v == "true" || v == "false"
	if !needsQuote {
		return v
	}
	escaped := strings.ReplaceAll(v, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	escaped = strings.ReplaceAll(escaped, "\n", `\n`)
	escaped = strings.ReplaceAll(escaped, "\r", `\r`)
	escaped = strings.ReplaceAll(escaped, "\t", `\t`)
	return `"` + escaped + `"`
}

// readNote parses one note: the frontmatter is authoritative for every field,
// and the body's Claim/Evidence lines fill in anything the header omitted
// (a note the user wrote by hand may have no frontmatter at all).
func (s *MarkdownVaultStore) readNote(path string) (usmf.CanonicalMemoryRecord, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return usmf.CanonicalMemoryRecord{}, err
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	fm, body := splitFrontmatter(text)
	var r usmf.CanonicalMemoryRecord
	for k, v := range fm {
		switch k {
		case "memory_id":
			r.MemoryID = v
		case "topic":
			r.Topic = v
		case "claim":
			r.Claim = v
		case "evidence_text":
			r.EvidenceText = v
		case "verdict":
			r.Verdict = v
		case "relation_edge":
			r.RelationEdge = v
		case "valid_from":
			r.ValidFrom = v
		case "invalid_at":
			r.InvalidAt = v
		case "source_url":
			r.SourceURL = v
		case "source_domain":
			r.SourceDomain = v
		case "authority_level":
			if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
				r.AuthorityLevel = n
			}
		case "embedding_model":
			r.EmbeddingModel = v
		}
	}
	parseNoteBody(body, &r)
	if r.MemoryID == "" {
		// A hand-written note has no header: its file name is its identity,
		// so the record still has a stable id and an upsert overwrites it
		// instead of creating a second copy.
		base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		if base == "" {
			return usmf.CanonicalMemoryRecord{}, errors.New("ragstore: note without a name")
		}
		r.MemoryID = base
	}
	if r.Topic == "" {
		r.Topic = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	return r, nil
}

// splitFrontmatter separates a leading "---" block from the body. A file
// with no such block is all body.
func splitFrontmatter(text string) (map[string]string, string) {
	fm := map[string]string{}
	if !strings.HasPrefix(text, "---\n") && text != "---" {
		return fm, text
	}
	rest := strings.TrimPrefix(text, "---\n")
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return fm, text
	}
	header := rest[:end]
	body := rest[end+len("\n---"):]
	body = strings.TrimPrefix(body, "\n")
	for _, line := range strings.Split(header, "\n") {
		line = strings.TrimRight(line, " \t")
		if line == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		fm[strings.TrimSpace(k)] = unquoteYAML(strings.TrimSpace(v))
	}
	return fm, body
}

// unquoteYAML removes the quoting yamlScalar may have added. It is
// deliberately small: the header is a flat map of scalars, so no nesting,
// anchors or block scalars are involved.
func unquoteYAML(v string) string {
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		inner := v[1 : len(v)-1]
		var b strings.Builder
		for i := 0; i < len(inner); i++ {
			if inner[i] == '\\' && i+1 < len(inner) {
				i++
				switch inner[i] {
				case 'n':
					b.WriteByte('\n')
				case 'r':
					b.WriteByte('\r')
				case 't':
					b.WriteByte('\t')
				default:
					b.WriteByte(inner[i])
				}
				continue
			}
			b.WriteByte(inner[i])
		}
		return b.String()
	}
	if len(v) >= 2 && v[0] == '\'' && v[len(v)-1] == '\'' {
		return v[1 : len(v)-1]
	}
	return v
}

// parseNoteBody reads the "- **Claim**: …" style lines a human or the
// renderer writes, so a note edited outside MolaSearch still yields its
// fields. A value that spans lines (a claim with a newline in it, written out
// by renderNote) is joined back with the newline it had.
func parseNoteBody(body string, r *usmf.CanonicalMemoryRecord) {
	lines := strings.Split(body, "\n")
	// collect joins a label's own line with the indented continuation lines
	// that follow it.
	collect := func(start int, first string) (string, int) {
		parts := []string{first}
		i := start + 1
		for ; i < len(lines); i++ {
			next := lines[i]
			if strings.TrimSpace(next) == "" {
				break
			}
			trimmed := strings.TrimLeft(next, " \t")
			// A continuation is indented and is not another field or bullet.
			if trimmed == next || strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "#") {
				break
			}
			parts = append(parts, trimmed)
		}
		return strings.Join(parts, "\n"), i - 1
	}
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, "# ") && r.Topic == "" {
			r.Topic = strings.TrimSpace(strings.TrimPrefix(line, "# "))
			continue
		}
		label, value, ok := strings.Cut(strings.TrimPrefix(line, "- "), "**:")
		if !ok || !strings.HasPrefix(line, "- **") {
			continue
		}
		label = strings.TrimPrefix(label, "**")
		value, last := collect(i, strings.TrimSpace(value))
		i = last
		switch strings.ToLower(strings.TrimSpace(label)) {
		case "claim":
			if r.Claim == "" {
				r.Claim = value
			}
		case "verified evidence", "evidence":
			if r.EvidenceText == "" {
				r.EvidenceText = value
			}
		case "verdict":
			if r.Verdict == "" {
				r.Verdict = value
			}
		case "source":
			if r.SourceURL == "" {
				r.SourceURL = value
			}
		}
	}
}
