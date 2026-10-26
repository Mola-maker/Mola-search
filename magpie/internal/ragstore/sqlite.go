package ragstore

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/yetone/magpie/internal/searchdet"
	_ "github.com/yetone/magpie/internal/sqlitedb" // libc environment guard, see its doc
	"github.com/yetone/magpie/internal/usmf"
)

// SQLiteHybridStore is MolaSearch's primary RAG backend: one SQLite file
// holding the records, their vectors, the relation edges and the agent's
// learned habits, searched with a real hybrid retrieval.
//
// Dense retrieval is a cosine scan over the stored vectors (a personal
// corpus is small enough that a scan beats maintaining an ANN index), lexical
// retrieval is FTS5's BM25 when the driver has FTS5 compiled in and an
// in-process Okapi BM25 index otherwise, and the two ranked lists are fused
// with reciprocal rank fusion. The temporal columns decide which records are
// current, so a superseded fact stops answering queries without being
// deleted.
type SQLiteHybridStore struct {
	id   string
	path string
	db   *sql.DB

	mu  sync.RWMutex
	emb Embedder
	// fts says whether the FTS5 virtual table exists. modernc's SQLite
	// builds normally enable FTS5, but a build without it must still work:
	// BM25 then comes from the in-process lexical index instead.
	fts bool
}

// SQLiteHybridID is the backend id Status and HotSwap use for the main store.
const SQLiteHybridID = "sqlite_hybrid"

// DefaultSQLitePath is where the main store lives unless a caller names
// another file: beside the rest of MolaSearch's state.
func DefaultSQLitePath() string {
	return filepath.Join(searchdet.DataDir(), "molasearch_hybrid.sqlite3")
}

// OpenSQLite opens (creating if needed) the hybrid store at path and makes
// sure its schema exists. It is safe to call twice on one file: every
// statement is idempotent.
func OpenSQLite(path string) (*SQLiteHybridStore, error) {
	return openSQLiteID(path, SQLiteHybridID)
}

func openSQLiteID(path, id string) (*SQLiteHybridStore, error) {
	if path == "" {
		path = DefaultSQLitePath()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	// busy_timeout lets a second process (the GUI and a CLI, or two app
	// windows) wait out the other's write instead of failing at once.
	dsn := "file:" + filepath.ToSlash(path) + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One writer at a time keeps SQLITE_BUSY away and the file portable;
	// SQLite's own WAL already allows concurrent readers.
	db.SetMaxOpenConns(4)
	s := &SQLiteHybridStore{id: id, path: path, db: db, emb: &LocalHashEmbedder{Dims: DefaultDims}}
	if err := s.initSchema(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *SQLiteHybridStore) initSchema() error {
	const base = `
CREATE TABLE IF NOT EXISTS memory_records (
	memory_id       TEXT PRIMARY KEY,
	topic           TEXT NOT NULL DEFAULT '',
	claim           TEXT NOT NULL DEFAULT '',
	evidence_text   TEXT NOT NULL DEFAULT '',
	verdict         TEXT NOT NULL DEFAULT '',
	relation_edge   TEXT NOT NULL DEFAULT '',
	valid_from      TEXT NOT NULL DEFAULT '',
	invalid_at      TEXT NOT NULL DEFAULT '',
	source_url      TEXT NOT NULL DEFAULT '',
	source_domain   TEXT NOT NULL DEFAULT '',
	authority_level INTEGER NOT NULL DEFAULT 0,
	embedding_model TEXT NOT NULL DEFAULT '',
	updated_at      TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS memory_vectors (
	memory_id  TEXT PRIMARY KEY,
	dim        INTEGER NOT NULL DEFAULT 0,
	vec        BLOB,
	model      TEXT NOT NULL DEFAULT '',
	updated_at TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS memory_edges (
	src        TEXT NOT NULL,
	dst        TEXT NOT NULL,
	edge_type  TEXT NOT NULL DEFAULT '',
	valid_from TEXT NOT NULL DEFAULT '',
	invalid_at TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (src, dst, edge_type)
);
CREATE TABLE IF NOT EXISTS agent_habits (
	pattern         TEXT PRIMARY KEY,
	mode_affinity   TEXT NOT NULL DEFAULT '{}',
	trusted_domains TEXT NOT NULL DEFAULT '[]',
	blocked_domains TEXT NOT NULL DEFAULT '[]',
	updated_at      TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS stakeout_jobs (
	id               TEXT PRIMARY KEY,
	title            TEXT NOT NULL DEFAULT '',
	query            TEXT NOT NULL DEFAULT '',
	cron             TEXT NOT NULL DEFAULT '',
	search_params    TEXT NOT NULL DEFAULT '',
	last_checked_at  TEXT NOT NULL DEFAULT '',
	diff_detected    INTEGER NOT NULL DEFAULT 0,
	status_badge     TEXT NOT NULL DEFAULT '',
	latest_diff      TEXT NOT NULL DEFAULT '',
	source_url       TEXT NOT NULL DEFAULT '',
	top_domains      TEXT NOT NULL DEFAULT '',
	sim_hash         INTEGER NOT NULL DEFAULT 0,
	interval_seconds INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS memory_records_valid ON memory_records(invalid_at);
CREATE INDEX IF NOT EXISTS memory_edges_dst ON memory_edges(dst);
`
	if _, err := s.db.Exec(base); err != nil {
		return err
	}
	if err := s.migrateStakeoutParams(); err != nil {
		return err
	}
	// FTS5 is a compile-time option. Try it once and remember the answer:
	// the alternative (an error on every search) would be worse than the
	// in-process BM25 fallback. The table holds the raw text and FTS5's
	// default tokenizer indexes it; CJK queries don't use this table because
	// that tokenizer has no CJK segmenter (see ftsRank).
	fts := true
	if _, err := s.db.Exec(`
CREATE VIRTUAL TABLE IF NOT EXISTS memory_fts USING fts5(
	memory_id UNINDEXED,
	topic,
	claim,
	evidence_text
);`); err != nil {
		fts = false
	}
	if fts {
		// Keep the index in step with the table. A separate FTS5 table (not
		// external content) means these triggers are the only coupling, so
		// a delete or an update can't leave a stale row behind.
		triggers := []string{
			`CREATE TRIGGER IF NOT EXISTS memory_fts_ai AFTER INSERT ON memory_records BEGIN
				INSERT INTO memory_fts(memory_id, topic, claim, evidence_text)
				VALUES (new.memory_id, new.topic, new.claim, new.evidence_text);
			END`,
			`CREATE TRIGGER IF NOT EXISTS memory_fts_ad AFTER DELETE ON memory_records BEGIN
				DELETE FROM memory_fts WHERE memory_id = old.memory_id;
			END`,
			`CREATE TRIGGER IF NOT EXISTS memory_fts_au AFTER UPDATE ON memory_records BEGIN
				DELETE FROM memory_fts WHERE memory_id = old.memory_id;
				INSERT INTO memory_fts(memory_id, topic, claim, evidence_text)
				VALUES (new.memory_id, new.topic, new.claim, new.evidence_text);
			END`,
		}
		for _, q := range triggers {
			if _, err := s.db.Exec(q); err != nil {
				fts = false
				break
			}
		}
	}
	s.mu.Lock()
	s.fts = fts
	s.mu.Unlock()
	return nil
}

// ID implements Store.
func (s *SQLiteHybridStore) ID() string { return s.id }

// Path is the database file this store owns, for Status's DSN column.
func (s *SQLiteHybridStore) Path() string { return s.path }

// SetEmbedder implements embedderSetter: HotSwap hands the store the model
// the migrated corpus was embedded with.
func (s *SQLiteHybridStore) SetEmbedder(e Embedder) {
	if e == nil {
		return
	}
	s.mu.Lock()
	s.emb = e
	s.mu.Unlock()
}

func (s *SQLiteHybridStore) embedder() Embedder {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.emb == nil {
		return &LocalHashEmbedder{Dims: DefaultDims}
	}
	return s.emb
}

// UsesFTS5 reports whether lexical retrieval goes through SQLite's FTS5
// table rather than the in-process BM25 index. Status surfaces it so the
// degradation is visible instead of silent.
func (s *SQLiteHybridStore) UsesFTS5() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.fts
}

// Ping implements Store.
func (s *SQLiteHybridStore) Ping(ctx context.Context) error {
	var one int
	return s.db.QueryRowContext(ctx, `SELECT 1`).Scan(&one)
}

// Count reports the real number of stored records.
func (s *SQLiteHybridStore) Count(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM memory_records`).Scan(&n)
	return n, err
}

// Upsert implements Store. Records, their vectors and their relation edges
// are written in one transaction, so a search never sees a record without
// its vector.
func (s *SQLiteHybridStore) Upsert(ctx context.Context, recs []usmf.CanonicalMemoryRecord) error {
	if len(recs) == 0 {
		return nil
	}
	emb := s.embedder()
	now := time.Now().UTC().Format(time.RFC3339)

	texts := make([]string, len(recs))
	for i, r := range recs {
		texts[i] = documentText(fieldsOf(r.Topic, r.Claim, r.EvidenceText))
	}
	vecs, err := emb.EmbedBatch(ctx, texts)
	if err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	recStmt, err := tx.PrepareContext(ctx, `
INSERT INTO memory_records
	(memory_id, topic, claim, evidence_text, verdict, relation_edge, valid_from,
	 invalid_at, source_url, source_domain, authority_level, embedding_model, updated_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(memory_id) DO UPDATE SET
	topic=excluded.topic, claim=excluded.claim, evidence_text=excluded.evidence_text,
	verdict=excluded.verdict, relation_edge=excluded.relation_edge,
	valid_from=excluded.valid_from, invalid_at=excluded.invalid_at,
	source_url=excluded.source_url, source_domain=excluded.source_domain,
	authority_level=excluded.authority_level, embedding_model=excluded.embedding_model,
	updated_at=excluded.updated_at`)
	if err != nil {
		return err
	}
	defer recStmt.Close()

	vecStmt, err := tx.PrepareContext(ctx, `
INSERT INTO memory_vectors (memory_id, dim, vec, model, updated_at)
VALUES (?,?,?,?,?)
ON CONFLICT(memory_id) DO UPDATE SET
	dim=excluded.dim, vec=excluded.vec, model=excluded.model, updated_at=excluded.updated_at`)
	if err != nil {
		return err
	}
	defer vecStmt.Close()

	edgeStmt, err := tx.PrepareContext(ctx, `
INSERT INTO memory_edges (src, dst, edge_type, valid_from, invalid_at)
VALUES (?,?,?,?,?)
ON CONFLICT(src, dst, edge_type) DO UPDATE SET
	valid_from=excluded.valid_from, invalid_at=excluded.invalid_at`)
	if err != nil {
		return err
	}
	defer edgeStmt.Close()

	for i, r := range recs {
		if strings.TrimSpace(r.MemoryID) == "" {
			return errors.New("ragstore: record with empty memoryId")
		}
		model := r.EmbeddingModel
		if model == "" {
			model = emb.ID()
		}
		if _, err := recStmt.ExecContext(ctx,
			r.MemoryID, r.Topic, r.Claim, r.EvidenceText, r.Verdict, r.RelationEdge,
			r.ValidFrom, r.InvalidAt, r.SourceURL, r.SourceDomain, r.AuthorityLevel,
			model, now); err != nil {
			return fmt.Errorf("ragstore: upsert %s: %w", r.MemoryID, err)
		}
		var vec []float32
		if i < len(vecs) {
			vec = vecs[i]
		}
		if _, err := vecStmt.ExecContext(ctx, r.MemoryID, len(vec), encodeVector(vec), model, now); err != nil {
			return fmt.Errorf("ragstore: vector %s: %w", r.MemoryID, err)
		}
		for _, e := range parseRelationEdges(r.RelationEdge) {
			if _, err := edgeStmt.ExecContext(ctx, r.MemoryID, e.dst, e.typ, r.ValidFrom, r.InvalidAt); err != nil {
				return fmt.Errorf("ragstore: edge %s: %w", r.MemoryID, err)
			}
		}
	}
	return tx.Commit()
}

// All implements Store, ordered by memory id so exports and hashes are
// reproducible.
func (s *SQLiteHybridStore) All(ctx context.Context) ([]usmf.CanonicalMemoryRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT memory_id, topic, claim, evidence_text, verdict, relation_edge, valid_from,
       invalid_at, source_url, source_domain, authority_level, embedding_model
FROM memory_records ORDER BY memory_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []usmf.CanonicalMemoryRecord
	for rows.Next() {
		var r usmf.CanonicalMemoryRecord
		if err := rows.Scan(&r.MemoryID, &r.Topic, &r.Claim, &r.EvidenceText, &r.Verdict,
			&r.RelationEdge, &r.ValidFrom, &r.InvalidAt, &r.SourceURL, &r.SourceDomain,
			&r.AuthorityLevel, &r.EmbeddingModel); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Search implements Store with the real hybrid retrieval described on the
// type: cosine over stored vectors, BM25 over the same text, and RRF fusion.
// Only records current at this moment take part: a record whose invalid_at
// has passed, or whose valid_from is still in the future, is skipped.
func (s *SQLiteHybridStore) Search(ctx context.Context, query string, qvec []float32, k int) ([]Hit, error) {
	k = topK(k)
	recs, vecs, err := s.loadAllWithVectors(ctx)
	if err != nil {
		return nil, err
	}
	if len(recs) == 0 {
		return nil, nil
	}
	if len(qvec) == 0 {
		qvec, err = s.embedder().Embed(ctx, query)
		if err != nil {
			return nil, err
		}
	}

	now := time.Now().UTC()
	valid := make(map[string]bool, len(recs))
	validRecs := make([]usmf.CanonicalMemoryRecord, 0, len(recs))
	for _, r := range recs {
		if recordValid(r, now) {
			valid[r.MemoryID] = true
			validRecs = append(validRecs, r)
		}
	}
	if len(validRecs) == 0 {
		return nil, nil
	}

	// Dense ranking: cosine against every current record's stored vector.
	cosine := make(map[string]float64, len(validRecs))
	dense := make([]scoredID, 0, len(validRecs))
	for _, r := range validRecs {
		c := CosineSimilarity(qvec, vecs[r.MemoryID])
		cosine[r.MemoryID] = c
		dense = append(dense, scoredID{r.MemoryID, c})
	}
	sort.SliceStable(dense, func(i, j int) bool {
		if dense[i].score != dense[j].score {
			return dense[i].score > dense[j].score
		}
		return dense[i].id < dense[j].id
	})
	denseIDs := idsOf(dense)

	// Lexical ranking: FTS5 BM25 when available, in-process BM25 otherwise.
	bm25Scores := make(map[string]float64, len(validRecs))
	var bm25IDs []string
	if s.UsesFTS5() {
		bm25IDs, bm25Scores, err = s.ftsRank(ctx, query, valid)
		if err != nil {
			return nil, err
		}
	}
	if bm25IDs == nil {
		// ftsRank may have left its scores nil when it declined the query
		// (CJK has no segmenter in this build); the in-process index owns
		// the map from here.
		bm25Scores = make(map[string]float64, len(validRecs))
		docs := make([]lexicalDoc, len(validRecs))
		for i, r := range validRecs {
			docs[i] = makeLexicalDoc(r.MemoryID, documentText(fieldsOf(r.Topic, r.Claim, r.EvidenceText)))
		}
		ix := newLexicalIndex(docs)
		bm25IDs = ix.rankDocuments(query)
		terms := Tokenize(query)
		for _, d := range docs {
			if score := ix.score(terms, d); score > 0 {
				bm25Scores[d.id] = score
			}
		}
	}

	fused := fuseRanks(denseIDs, bm25IDs)
	byID := make(map[string]usmf.CanonicalMemoryRecord, len(validRecs))
	for _, r := range validRecs {
		byID[r.MemoryID] = r
	}
	ranked := make([]scoredID, 0, len(fused))
	for id, f := range fused {
		if _, ok := byID[id]; ok {
			ranked = append(ranked, scoredID{id, f})
		}
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
		hits = append(hits, Hit{
			Record: byID[s.id],
			Cosine: cosine[s.id],
			BM25:   bm25Scores[s.id],
			RRF:    s.score,
		})
	}
	return hits, nil
}

// ftsRank asks FTS5 for its BM25 ranking. It returns nil when FTS5 can't
// answer, which sends the caller to the in-process Okapi BM25 instead.
//
// A query with CJK in it always takes that path. SQLite's built-in tokenizer
// treats a whole unspaced CJK run as one token, so "电池续航" in a document is
// a single term and would never match the user's "电池": FTS5 has no CJK
// segmenter in this build. The in-process index tokenises CJK into character
// bigrams and trigrams, so it is the one that can answer those queries.
func (s *SQLiteHybridStore) ftsRank(ctx context.Context, query string, valid map[string]bool) ([]string, map[string]float64, error) {
	if hasCJK(query) {
		return nil, nil, nil
	}
	match := ftsQuery(query)
	if match == "" {
		return nil, nil, nil
	}
	// bm25() is negative and smaller is better; asking for the whole ranking
	// (bounded well above any topK) lets RRF see the same list the dense
	// side sees.
	rows, err := s.db.QueryContext(ctx, `
SELECT memory_id, bm25(memory_fts) AS score
FROM memory_fts WHERE memory_fts MATCH ?
ORDER BY score LIMIT 1000`, match)
	if err != nil {
		// A bad MATCH is the caller's text, not a broken database: degrade.
		return nil, nil, nil
	}
	defer rows.Close()
	var ids []string
	scores := make(map[string]float64)
	for rows.Next() {
		var id string
		var v float64
		if err := rows.Scan(&id, &v); err != nil {
			return nil, nil, err
		}
		if !valid[id] {
			continue
		}
		ids = append(ids, id)
		// SQLite's bm25() returns a negative number whose magnitude is the
		// score; flipping the sign gives the usual "bigger is better".
		scores[id] = -v
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return ids, scores, nil
}

func (s *SQLiteHybridStore) loadAllWithVectors(ctx context.Context) ([]usmf.CanonicalMemoryRecord, map[string][]float32, error) {
	recs, err := s.All(ctx)
	if err != nil {
		return nil, nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT memory_id, vec FROM memory_vectors`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	vecs := make(map[string][]float32, len(recs))
	for rows.Next() {
		var id string
		var blob []byte
		if err := rows.Scan(&id, &blob); err != nil {
			return nil, nil, err
		}
		vecs[id] = decodeVector(blob)
	}
	return recs, vecs, rows.Err()
}

// Close implements Store.
func (s *SQLiteHybridStore) Close() error { return s.db.Close() }

// VectorFor returns the stored vector of one record, for callers that need
// it outside a search (the hot-swap verification does).
func (s *SQLiteHybridStore) VectorFor(ctx context.Context, memoryID string) ([]float32, bool, error) {
	var blob []byte
	err := s.db.QueryRowContext(ctx, `SELECT vec FROM memory_vectors WHERE memory_id = ?`, memoryID).Scan(&blob)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return decodeVector(blob), true, nil
}

// Neighbors returns the memory ids one hop away from id through the stored
// relation edges, and the edges themselves. Neo4j's search expands through
// this so a graph backend answers with the mirror's real edges.
func (s *SQLiteHybridStore) Neighbors(ctx context.Context, id string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT dst FROM memory_edges WHERE src = ? AND (invalid_at = '' OR invalid_at > ?)
UNION
SELECT src FROM memory_edges WHERE dst = ? AND (invalid_at = '' OR invalid_at > ?)`,
		id, time.Now().UTC().Format(time.RFC3339), id, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// --- habits -------------------------------------------------------------

// loadHabits reads every learned habit.
func (s *SQLiteHybridStore) loadHabits(ctx context.Context) ([]Habit, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT pattern, mode_affinity, trusted_domains, blocked_domains, updated_at
FROM agent_habits ORDER BY pattern`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Habit
	for rows.Next() {
		var h Habit
		var affinity, trusted, blocked string
		if err := rows.Scan(&h.Pattern, &affinity, &trusted, &blocked, &h.UpdatedAt); err != nil {
			return nil, err
		}
		h.ModeAffinity = map[string]float64{}
		if affinity != "" {
			// A malformed column must not lose the other habits: an empty
			// affinity map is the honest reading of "unparseable".
			_ = json.Unmarshal([]byte(affinity), &h.ModeAffinity)
		}
		if h.ModeAffinity == nil {
			h.ModeAffinity = map[string]float64{}
		}
		h.TrustedDomains = decodeStrings(trusted)
		h.BlockedDomains = decodeStrings(blocked)
		out = append(out, h)
	}
	return out, rows.Err()
}

// saveHabit writes one habit, merging nothing: the caller has already read
// and updated it.
func (s *SQLiteHybridStore) saveHabit(ctx context.Context, h Habit) error {
	affinity, err := json.Marshal(h.ModeAffinity)
	if err != nil {
		return err
	}
	trusted, err := json.Marshal(h.TrustedDomains)
	if err != nil {
		return err
	}
	blocked, err := json.Marshal(h.BlockedDomains)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO agent_habits (pattern, mode_affinity, trusted_domains, blocked_domains, updated_at)
VALUES (?,?,?,?,?)
ON CONFLICT(pattern) DO UPDATE SET
	mode_affinity=excluded.mode_affinity, trusted_domains=excluded.trusted_domains,
	blocked_domains=excluded.blocked_domains, updated_at=excluded.updated_at`,
		h.Pattern, string(affinity), string(trusted), string(blocked), h.UpdatedAt)
	return err
}

func decodeStrings(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil
	}
	return out
}

// migrateStakeoutParams renames the old vendor-named column on a database
// created before the provider registry replaced it. A fresh database has
// only search_params, so this is a no-op there.
func (s *SQLiteHybridStore) migrateStakeoutParams() error {
	rows, err := s.db.Query(`PRAGMA table_info(stakeout_jobs)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	old, fresh := false, false
	for rows.Next() {
		var (
			cid     int
			name    string
			typ     string
			notNull int
			dflt    any
			pk      int
		)
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			return err
		}
		switch name {
		case "yuanbao_params":
			old = true
		case "search_params":
			fresh = true
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if old && !fresh {
		_, err = s.db.Exec(`ALTER TABLE stakeout_jobs RENAME COLUMN yuanbao_params TO search_params`)
	}
	return err
}

// --- stakeouts ----------------------------------------------------------

func (s *SQLiteHybridStore) stakeoutList(ctx context.Context) ([]searchdet.StakeoutJob, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, title, query, cron, search_params, last_checked_at, diff_detected,
       status_badge, latest_diff, source_url, top_domains, sim_hash, interval_seconds
FROM stakeout_jobs ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []searchdet.StakeoutJob
	for rows.Next() {
		var j searchdet.StakeoutJob
		var diff int
		var sim int64
		if err := rows.Scan(&j.ID, &j.Title, &j.Query, &j.Cron, &j.SearchParams,
			&j.LastCheckedAt, &diff, &j.StatusBadge, &j.LatestDiff, &j.SourceURL,
			&j.TopDomains, &sim, &j.Interval); err != nil {
			return nil, err
		}
		j.DiffDetected = diff != 0
		// SQLite's INTEGER is signed; the snapshot hash is unsigned, so it
		// is stored two's-complement and read back the same way.
		j.SimHash = uint64(sim)
		out = append(out, j)
	}
	return out, rows.Err()
}

func (s *SQLiteHybridStore) stakeoutSave(ctx context.Context, j searchdet.StakeoutJob) error {
	if strings.TrimSpace(j.ID) == "" {
		return errors.New("ragstore: stakeout job with empty id")
	}
	diff := 0
	if j.DiffDetected {
		diff = 1
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO stakeout_jobs
	(id, title, query, cron, search_params, last_checked_at, diff_detected,
	 status_badge, latest_diff, source_url, top_domains, sim_hash, interval_seconds)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET
	title=excluded.title, query=excluded.query, cron=excluded.cron,
	search_params=excluded.search_params, last_checked_at=excluded.last_checked_at,
	diff_detected=excluded.diff_detected, status_badge=excluded.status_badge,
	latest_diff=excluded.latest_diff, source_url=excluded.source_url,
	top_domains=excluded.top_domains, sim_hash=excluded.sim_hash,
	interval_seconds=excluded.interval_seconds`,
		j.ID, j.Title, j.Query, j.Cron, j.SearchParams, j.LastCheckedAt, diff,
		j.StatusBadge, j.LatestDiff, j.SourceURL, j.TopDomains, int64(j.SimHash), j.Interval)
	return err
}

func (s *SQLiteHybridStore) stakeoutDelete(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM stakeout_jobs WHERE id = ?`, id)
	return err
}

func (s *SQLiteHybridStore) stakeoutGet(ctx context.Context, id string) (searchdet.StakeoutJob, bool, error) {
	var j searchdet.StakeoutJob
	var diff int
	var sim int64
	err := s.db.QueryRowContext(ctx, `
SELECT id, title, query, cron, search_params, last_checked_at, diff_detected,
       status_badge, latest_diff, source_url, top_domains, sim_hash, interval_seconds
FROM stakeout_jobs WHERE id = ?`, id).Scan(&j.ID, &j.Title, &j.Query, &j.Cron,
		&j.SearchParams, &j.LastCheckedAt, &diff, &j.StatusBadge, &j.LatestDiff,
		&j.SourceURL, &j.TopDomains, &sim, &j.Interval)
	if errors.Is(err, sql.ErrNoRows) {
		return searchdet.StakeoutJob{}, false, nil
	}
	if err != nil {
		return searchdet.StakeoutJob{}, false, err
	}
	j.DiffDetected = diff != 0
	j.SimHash = uint64(sim)
	return j, true, nil
}

// --- helpers ------------------------------------------------------------

type scoredID struct {
	id    string
	score float64
}

func idsOf(ss []scoredID) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = s.id
	}
	return out
}

// recordValid applies the bitemporal rule: a record answers a search only
// while it is in force. An empty bound is open. A bound that doesn't parse
// is unknown, and unknown counts as in force, because hiding a fact on a
// malformed date would silently lose evidence.
func recordValid(r usmf.CanonicalMemoryRecord, now time.Time) bool {
	if r.ValidFrom != "" {
		if t, ok := parseTime(r.ValidFrom); ok && t.After(now) {
			return false
		}
	}
	if r.InvalidAt != "" {
		if t, ok := parseTime(r.InvalidAt); ok && !t.After(now) {
			return false
		}
	}
	return true
}

func parseTime(s string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, strings.TrimSpace(s)); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// hasCJK reports whether text contains a CJK character, which is the signal
// that FTS5's unsegmented tokenizer can't serve the query.
func hasCJK(text string) bool {
	for _, r := range text {
		if isCJK(r) {
			return true
		}
	}
	return false
}

// ftsQuery turns a user's text into an FTS5 MATCH expression: every token,
// quoted, OR-joined. Quoting makes the token a phrase and keeps FTS5's own
// operators out of user input, so a query can't turn into a syntax error
// (or a query the user didn't write).
func ftsQuery(query string) string {
	tokens := Tokenize(query)
	if len(tokens) == 0 {
		return ""
	}
	seen := make(map[string]bool, len(tokens))
	parts := make([]string, 0, len(tokens))
	for _, t := range tokens {
		if seen[t] {
			continue
		}
		seen[t] = true
		parts = append(parts, `"`+strings.ReplaceAll(t, `"`, `""`)+`"`)
	}
	return strings.Join(parts, " OR ")
}

// edgeSpec is one parsed relation: this record points at dst with typ.
type edgeSpec struct {
	typ string
	dst string
}

// parseRelationEdges reads the relation_edge column. The format is
// comma-separated entries of "TYPE:TARGET_ID" or "TYPE->TARGET_ID"; an entry
// without a target (a bare label) records no edge, since an edge needs both
// ends.
func parseRelationEdges(spec string) []edgeSpec {
	if strings.TrimSpace(spec) == "" {
		return nil
	}
	var out []edgeSpec
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		var typ, dst string
		if a, b, ok := strings.Cut(part, "->"); ok {
			typ, dst = a, b
		} else if a, b, ok := strings.Cut(part, ":"); ok {
			typ, dst = a, b
		} else {
			continue
		}
		typ, dst = strings.TrimSpace(typ), strings.TrimSpace(dst)
		if typ == "" || dst == "" {
			continue
		}
		out = append(out, edgeSpec{typ: typ, dst: dst})
	}
	return out
}

// encodeVector writes floats little-endian: fixed width, no parsing on read,
// and portable across the architectures MolaSearch runs on.
func encodeVector(v []float32) []byte {
	buf := make([]byte, 4*len(v))
	for i, f := range v {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return buf
}

func decodeVector(b []byte) []float32 {
	if len(b)%4 != 0 {
		return nil
	}
	out := make([]float32, len(b)/4)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return out
}
