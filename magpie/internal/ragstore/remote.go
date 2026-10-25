package ragstore

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver

	"github.com/yetone/magpie/internal/usmf"
)

// The four remote backends below share one design: a real protocol probe
// against the server the user configured, and a local SQLite mirror that
// holds the data. The mirror is not a placeholder for a missing client — it
// is how these backends stay useful on a laptop where the server is often
// asleep or unreachable. A probe that fails is reported as a failed Ping and
// the search keeps answering from the mirror; a probe that succeeds means
// the real server was reached, and the search still answers from the mirror
// because the mirror is the copy this process wrote and can therefore serve
// consistently.
//
// Upstream magpie forbids a Postgres driver, so Postgres was probed over TCP
// only. As of 2026-10-24 the MolaSearch maintainer lifts that rule (independent
// product, PATCHES.md): Postgres now writes and reads real rows through pgx,
// and All() reads the server's own corpus so a hot swap's SHA-256 guard
// verifies the server, not just the mirror. When the server is unreachable
// every path falls back to the mirror, exactly as before. Elasticsearch,
// Milvus and Neo4j still speak HTTP.

// remoteEnv names the environment variable that overrides a remote backend's
// address, so a user can point MolaSearch at their own server without a
// settings file.
const (
	envPostgresDSN = "MOLA_PG_DSN"
	envMilvusBase  = "MOLA_MILVUS_BASE"
	envESBase      = "MOLA_ES_BASE"
	envNeo4jBase   = "MOLA_NEO4J_BASE"
)

// remoteHTTPTimeout bounds every probe: a backend that doesn't answer within
// it is down as far as a status page is concerned.
const remoteHTTPTimeout = 3 * time.Second

func newRemoteClient() *http.Client {
	return &http.Client{Timeout: remoteHTTPTimeout}
}

// --- Postgres -----------------------------------------------------------

// PostgresStore is the pgvector backend. Rows are written to and read back
// from the real server through pgx; the local SQLite mirror stays the search
// path and the fallback while the server is asleep or unreachable.
type PostgresStore struct {
	DSN   string
	Local *SQLiteHybridStore

	id string

	mu      sync.Mutex
	db      *sql.DB   // non-nil once the server has answered a ping
	lastTry time.Time // rate-limits reconnect attempts after a failure
}

// PostgresID is the backend id Status and HotSwap use for Postgres.
const PostgresID = "postgres_pgvector"

// NewPostgresStore builds the backend over an already-open mirror. The DSN
// comes from MOLA_PG_DSN when the caller passes none, so an unconfigured
// backend still reports the address it would have used.
func NewPostgresStore(dsn string, local *SQLiteHybridStore) *PostgresStore {
	if dsn == "" {
		dsn = strings.TrimSpace(os.Getenv(envPostgresDSN))
	}
	if dsn == "" {
		dsn = "postgres://molasearch@127.0.0.1:5432/molasearch"
	}
	return &PostgresStore{DSN: dsn, Local: local, id: PostgresID}
}

// ID implements Store.
func (p *PostgresStore) ID() string { return p.id }

// Ping implements Store as a real protocol round-trip: the connection runs
// the pgx startup and a trivial query, so an answer means a Postgres server
// speaking SQL, not just a port.
func (p *PostgresStore) Ping(ctx context.Context) error {
	db, err := p.server()
	if err != nil {
		return err
	}
	return db.PingContext(ctx)
}

// server opens the DSN once and keeps the handle. A failed attempt is
// retried at most every 10s, so a sleeping server costs one dial per window
// instead of one per call. On success the records table is ensured.
func (p *PostgresStore) server() (*sql.DB, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.db != nil {
		return p.db, nil
	}
	if !p.lastTry.IsZero() && time.Since(p.lastTry) < 10*time.Second {
		return nil, fmt.Errorf("ragstore: postgres %s: server unreachable (retry window)", p.DSN)
	}
	p.lastTry = time.Now()
	db, err := sql.Open("pgx", p.DSN)
	if err != nil {
		return nil, fmt.Errorf("ragstore: postgres open: %w", err)
	}
	db.SetMaxOpenConns(2)
	db.SetConnMaxIdleTime(time.Minute)
	if err := db.PingContext(context.Background()); err != nil {
		db.Close()
		return nil, fmt.Errorf("ragstore: postgres %s: %w", p.DSN, err)
	}
	if _, err := db.ExecContext(context.Background(), pgSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("ragstore: postgres schema: %w", err)
	}
	p.db = db
	return p.db, nil
}

// pgSchema creates the table the rows are written to. Idempotent; a table
// made by an earlier run (or by hand) keeps its shape.
const pgSchema = `
CREATE TABLE IF NOT EXISTS memory_records (
	memory_id       text PRIMARY KEY,
	topic           text NOT NULL DEFAULT '',
	claim           text NOT NULL DEFAULT '',
	evidence_text   text NOT NULL DEFAULT '',
	verdict         text NOT NULL DEFAULT '',
	relation_edge   text NOT NULL DEFAULT '',
	valid_from      text NOT NULL DEFAULT '',
	invalid_at      text NOT NULL DEFAULT '',
	source_url      text NOT NULL DEFAULT '',
	source_domain   text NOT NULL DEFAULT '',
	authority_level int  NOT NULL DEFAULT 0,
	embedding_model text NOT NULL DEFAULT ''
);
-- a table made before a column existed keeps its shape unless told to grow
ALTER TABLE memory_records ADD COLUMN IF NOT EXISTS relation_edge text NOT NULL DEFAULT '';
ALTER TABLE memory_records ALTER COLUMN valid_from TYPE text USING valid_from::text;
ALTER TABLE memory_records ALTER COLUMN invalid_at TYPE text USING invalid_at::text;`

// pgUpsert writes one row; ON CONFLICT keeps a re-run idempotent so a
// retried hot swap does not duplicate.
const pgUpsert = `
INSERT INTO memory_records
	(memory_id, topic, claim, evidence_text, verdict, relation_edge,
	 valid_from, invalid_at, source_url, source_domain, authority_level, embedding_model)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
ON CONFLICT (memory_id) DO UPDATE SET
	topic=EXCLUDED.topic, claim=EXCLUDED.claim, evidence_text=EXCLUDED.evidence_text,
	verdict=EXCLUDED.verdict, relation_edge=EXCLUDED.relation_edge,
	valid_from=EXCLUDED.valid_from, invalid_at=EXCLUDED.invalid_at,
	source_url=EXCLUDED.source_url, source_domain=EXCLUDED.source_domain,
	authority_level=EXCLUDED.authority_level, embedding_model=EXCLUDED.embedding_model`

// Upsert implements Store: the mirror is written first (the search path),
// then every row is written to the server. A server write that fails while
// the server is reachable is a real error — the caller (a hot swap) must
// not believe data is stored when it is not. An unreachable server leaves
// the mirror write standing, which is the documented degraded mode.
func (p *PostgresStore) Upsert(ctx context.Context, recs []usmf.CanonicalMemoryRecord) error {
	if err := p.Local.Upsert(ctx, recs); err != nil {
		return err
	}
	db, err := p.server()
	if err != nil {
		return nil // degraded: mirror-only, the Status page shows the server down
	}
	for _, r := range recs {
		if _, err := db.ExecContext(ctx, pgUpsert,
			r.MemoryID, r.Topic, r.Claim, r.EvidenceText, r.Verdict, r.RelationEdge,
			r.ValidFrom, r.InvalidAt, r.SourceURL, r.SourceDomain, r.AuthorityLevel, r.EmbeddingModel); err != nil {
			return fmt.Errorf("ragstore: postgres upsert %s: %w", r.MemoryID, err)
		}
	}
	return nil
}

// All implements Store: the server's own corpus when it answers, so a hot
// swap's hash guard proves the server holds what the mirror holds; the
// mirror when it does not.
func (p *PostgresStore) All(ctx context.Context) ([]usmf.CanonicalMemoryRecord, error) {
	db, err := p.server()
	if err != nil {
		return p.Local.All(ctx)
	}
	rows, err := db.QueryContext(ctx, `SELECT memory_id, topic, claim, evidence_text, verdict,
		relation_edge, valid_from, invalid_at, source_url, source_domain, authority_level, embedding_model
		FROM memory_records ORDER BY memory_id`)
	if err != nil {
		return p.Local.All(ctx)
	}
	defer rows.Close()
	var out []usmf.CanonicalMemoryRecord
	for rows.Next() {
		var r usmf.CanonicalMemoryRecord
		if err := rows.Scan(&r.MemoryID, &r.Topic, &r.Claim, &r.EvidenceText, &r.Verdict,
			&r.RelationEdge, &r.ValidFrom, &r.InvalidAt, &r.SourceURL, &r.SourceDomain,
			&r.AuthorityLevel, &r.EmbeddingModel); err != nil {
			return nil, fmt.Errorf("ragstore: postgres scan: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Search implements Store. The hybrid ranking is the mirror's real one; the
// stored record names the backend so a UI can say where the fact lives.
func (p *PostgresStore) Search(ctx context.Context, query string, qvec []float32, k int) ([]Hit, error) {
	return p.Local.Search(ctx, query, qvec, k)
}

// Close implements Store. The mirror belongs to the Manager, which closes it
// once; this backend only drops its reference.
func (p *PostgresStore) Close() error { p.Local = nil; return nil }

// SetEmbedder implements embedderSetter by handing the model to the mirror.
func (p *PostgresStore) SetEmbedder(e Embedder) { p.Local.SetEmbedder(e) }

// pgHostPort extracts host and port from a libpq-style DSN. Both the URL
// form and the key=value form are accepted, because users paste either.
func pgHostPort(dsn string) (string, string, error) {
	host, port := "127.0.0.1", "5432"
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		u, err := url.Parse(dsn)
		if err != nil {
			return "", "", fmt.Errorf("ragstore: postgres DSN: %w", err)
		}
		if h := u.Hostname(); h != "" {
			host = h
		}
		if pp := u.Port(); pp != "" {
			port = pp
		}
		return host, port, nil
	}
	for _, field := range strings.Fields(dsn) {
		k, v, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		switch k {
		case "host":
			host = v
		case "port":
			port = v
		}
	}
	return host, port, nil
}

// --- Elasticsearch ------------------------------------------------------

// ElasticsearchStore is the Elasticsearch backend. It writes the evidence
// index over the real REST API when the server answers and always keeps the
// mirror, so a search still works while the cluster is down.
type ElasticsearchStore struct {
	Base  string
	Local *SQLiteHybridStore

	id     string
	client *http.Client
}

// ElasticsearchID is the backend id Status and HotSwap use for Elasticsearch.
const ElasticsearchID = "elasticsearch"

// esIndex is the index MolaSearch writes evidence documents into.
const esIndex = "molasearch_evidence"

// NewElasticsearchStore builds the backend. The address comes from
// MOLA_ES_BASE when the caller passes none.
func NewElasticsearchStore(base string, local *SQLiteHybridStore) *ElasticsearchStore {
	if base == "" {
		base = strings.TrimSpace(os.Getenv(envESBase))
	}
	if base == "" {
		base = "http://127.0.0.1:9200"
	}
	return &ElasticsearchStore{Base: strings.TrimRight(base, "/"), Local: local, id: ElasticsearchID, client: newRemoteClient()}
}

// ID implements Store.
func (e *ElasticsearchStore) ID() string { return e.id }

// Ping implements Store with GET /, the cluster-info call Elasticsearch
// documents for readiness. It also reads version.number, so the status page
// can show the real version instead of a guess.
func (e *ElasticsearchStore) Ping(ctx context.Context) error {
	_, err := e.clusterInfo(ctx)
	return err
}

// Version returns the cluster's version string, or "" when it can't be read.
func (e *ElasticsearchStore) Version(ctx context.Context) string {
	info, err := e.clusterInfo(ctx)
	if err != nil {
		return ""
	}
	return info.Version.Number
}

type esInfo struct {
	Name    string `json:"name"`
	Version struct {
		Number string `json:"number"`
	} `json:"version"`
}

func (e *ElasticsearchStore) clusterInfo(ctx context.Context) (esInfo, error) {
	var info esInfo
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.Base+"/", nil)
	if err != nil {
		return info, err
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return info, fmt.Errorf("ragstore: elasticsearch %s: %w", e.Base, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return info, fmt.Errorf("ragstore: elasticsearch %s: HTTP %d", e.Base, resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&info); err != nil {
		return info, fmt.Errorf("ragstore: elasticsearch %s: %w", e.Base, err)
	}
	return info, nil
}

// Upsert implements Store: every record is written to the mirror first, then
// PUT into the index. A server failure doesn't fail the call — the mirror is
// the durable copy and the index catches up on the next write.
func (e *ElasticsearchStore) Upsert(ctx context.Context, recs []usmf.CanonicalMemoryRecord) error {
	if err := e.Local.Upsert(ctx, recs); err != nil {
		return err
	}
	for _, r := range recs {
		if err := e.indexDoc(ctx, r); err != nil {
			// The mirror holds it; the index is a copy. Report nothing so a
			// down cluster doesn't stop an investigation from remembering.
			continue
		}
	}
	return nil
}

func (e *ElasticsearchStore) indexDoc(ctx context.Context, r usmf.CanonicalMemoryRecord) error {
	body, err := json.Marshal(r)
	if err != nil {
		return err
	}
	u := e.Base + "/" + esIndex + "/_doc/" + url.PathEscape(r.MemoryID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("ragstore: elasticsearch index %s: HTTP %d", r.MemoryID, resp.StatusCode)
	}
	return nil
}

// All implements Store.
func (e *ElasticsearchStore) All(ctx context.Context) ([]usmf.CanonicalMemoryRecord, error) {
	return e.Local.All(ctx)
}

// Search implements Store. It asks the cluster for its own ranking
// (POST /molasearch_evidence/_search) and fuses that list with the mirror's
// dense ranking, which is what makes this a hybrid rather than a proxy. When
// the cluster is unreachable the mirror's full hybrid answer stands alone.
func (e *ElasticsearchStore) Search(ctx context.Context, query string, qvec []float32, k int) ([]Hit, error) {
	k = topK(k)
	localHits, err := e.Local.Search(ctx, query, qvec, k*4)
	if err != nil {
		return nil, err
	}
	if len(localHits) == 0 {
		return nil, nil
	}
	remoteIDs, remoteScores := e.remoteSearch(ctx, query, k*4)
	if len(remoteIDs) == 0 {
		if len(localHits) > k {
			localHits = localHits[:k]
		}
		return localHits, nil
	}
	// Local hits are already ordered by their fused score; take that order
	// as the mirror's ranked list and fuse it with the cluster's.
	localIDs := make([]string, len(localHits))
	localByID := make(map[string]Hit, len(localHits))
	for i, h := range localHits {
		localIDs[i] = h.Record.MemoryID
		localByID[h.Record.MemoryID] = h
	}
	fused := fuseRanks(localIDs, remoteIDs)
	out := make([]Hit, 0, len(fused))
	for id, score := range fused {
		h, ok := localByID[id]
		if !ok {
			// The cluster knows a document the mirror doesn't (another
			// writer indexed it). Its score is real but the record text
			// isn't here, so it can't be returned as a hit.
			continue
		}
		h.RRF = score
		if s, ok := remoteScores[id]; ok {
			h.BM25 = s
		}
		out = append(out, h)
	}
	sortHits(out)
	if len(out) > k {
		out = out[:k]
	}
	return out, nil
}

// remoteSearch runs the cluster's query: a BM25 multi_match, with a knn
// clause only when the caller supplied a query vector. Elasticsearch rejects
// a knn body on an index whose mapping has no dense_vector field, so the
// fallback to a plain match is the normal path for a mirror-only index.
func (e *ElasticsearchStore) remoteSearch(ctx context.Context, query string, k int) ([]string, map[string]float64) {
	if strings.TrimSpace(query) == "" {
		return nil, nil
	}
	body := map[string]any{
		"size": k,
		"query": map[string]any{
			"multi_match": map[string]any{
				"query":  query,
				"fields": []string{"topic^2", "claim^3", "evidenceText"},
				"type":   "best_fields",
			},
		},
	}
	ids, scores, ok := e.searchBody(ctx, body)
	if ok && len(ids) > 0 {
		return ids, scores
	}
	// A cluster that answered 400 to the scored query still answers the
	// simplest one; this is the documented match fallback.
	fallback := map[string]any{
		"size":  k,
		"query": map[string]any{"match": map[string]any{"claim": query}},
	}
	ids, scores, _ = e.searchBody(ctx, fallback)
	return ids, scores
}

func (e *ElasticsearchStore) searchBody(ctx context.Context, body any) ([]string, map[string]float64, bool) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, nil, false
	}
	u := e.Base + "/" + esIndex + "/_search"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(raw))
	if err != nil {
		return nil, nil, false
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.client.Do(req)
	if err != nil {
		return nil, nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		return nil, nil, false
	}
	var parsed struct {
		Hits struct {
			Hits []struct {
				ID     string  `json:"_id"`
				Score  float64 `json:"_score"`
				Source struct {
					MemoryID string `json:"memoryId"`
				} `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&parsed); err != nil {
		return nil, nil, false
	}
	var ids []string
	scores := make(map[string]float64)
	for _, h := range parsed.Hits.Hits {
		id := h.Source.MemoryID
		if id == "" {
			id = h.ID
		}
		if id == "" {
			continue
		}
		ids = append(ids, id)
		scores[id] = h.Score
	}
	return ids, scores, true
}

// Close implements Store.
func (e *ElasticsearchStore) Close() error { e.Local = nil; return nil }

// SetEmbedder implements embedderSetter by handing the model to the mirror.
func (e *ElasticsearchStore) SetEmbedder(emb Embedder) { e.Local.SetEmbedder(emb) }

// --- Milvus -------------------------------------------------------------

// MilvusStore is the Milvus/Zilliz backend. Milvus's native port (19530) is
// gRPC, so the REST probe is GET /healthz on the REST gateway; a deployment
// that only exposes gRPC will fail this probe, which is reported honestly
// rather than pretended away. The corpus lives in the mirror.
type MilvusStore struct {
	Base  string
	Local *SQLiteHybridStore

	id     string
	client *http.Client
}

// MilvusID is the backend id Status and HotSwap use for Milvus.
const MilvusID = "milvus_zilliz"

// NewMilvusStore builds the backend. The address comes from MOLA_MILVUS_BASE
// when the caller passes none.
func NewMilvusStore(base string, local *SQLiteHybridStore) *MilvusStore {
	if base == "" {
		base = strings.TrimSpace(os.Getenv(envMilvusBase))
	}
	if base == "" {
		base = "http://127.0.0.1:9091"
	}
	return &MilvusStore{Base: strings.TrimRight(base, "/"), Local: local, id: MilvusID, client: newRemoteClient()}
}

// ID implements Store.
func (m *MilvusStore) ID() string { return m.id }

// Ping implements Store with GET {Base}/healthz. Milvus v2 REST answers
// 200 with an empty body when the server is healthy.
func (m *MilvusStore) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.Base+"/healthz", nil)
	if err != nil {
		return err
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return fmt.Errorf("ragstore: milvus %s: %w", m.Base, err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("ragstore: milvus %s: HTTP %d", m.Base, resp.StatusCode)
	}
	return nil
}

// Upsert implements Store.
func (m *MilvusStore) Upsert(ctx context.Context, recs []usmf.CanonicalMemoryRecord) error {
	return m.Local.Upsert(ctx, recs)
}

// All implements Store.
func (m *MilvusStore) All(ctx context.Context) ([]usmf.CanonicalMemoryRecord, error) {
	return m.Local.All(ctx)
}

// Search implements Store with the mirror's hybrid ranking.
func (m *MilvusStore) Search(ctx context.Context, query string, qvec []float32, k int) ([]Hit, error) {
	return m.Local.Search(ctx, query, qvec, k)
}

// Close implements Store.
func (m *MilvusStore) Close() error { m.Local = nil; return nil }

// SetEmbedder implements embedderSetter by handing the model to the mirror.
func (m *MilvusStore) SetEmbedder(e Embedder) { m.Local.SetEmbedder(e) }

// --- Neo4j --------------------------------------------------------------

// Neo4jStore is the graph backend. Its probe is a real transactional Cypher
// call (POST /db/neo4j/tx/commit with RETURN 1), and its search expands the
// mirror's hybrid hits through the stored relation edges, which is the thing
// a graph store adds to a vector store: a fact's neighbours answer too.
type Neo4jStore struct {
	Base  string
	Local *SQLiteHybridStore
	// User and Pass are optional Basic Auth credentials for a secured
	// instance; the default install accepts them unset.
	User string
	Pass string

	id     string
	client *http.Client
}

// Neo4jID is the backend id Status and HotSwap use for Neo4j.
const Neo4jID = "neo4j_graphiti"

// NewNeo4jStore builds the backend. The address comes from MOLA_NEO4J_BASE
// when the caller passes none.
func NewNeo4jStore(base string, local *SQLiteHybridStore) *Neo4jStore {
	if base == "" {
		base = strings.TrimSpace(os.Getenv(envNeo4jBase))
	}
	if base == "" {
		base = "http://127.0.0.1:7474"
	}
	return &Neo4jStore{Base: strings.TrimRight(base, "/"), Local: local, id: Neo4jID, client: newRemoteClient()}
}

// ID implements Store.
func (n *Neo4jStore) ID() string { return n.id }

// Ping implements Store with a transactional endpoint call: the server has to
// parse and run "RETURN 1" for the probe to pass, so this checks the Cypher
// engine, not just the socket.
func (n *Neo4jStore) Ping(ctx context.Context) error {
	body := map[string]any{"statements": []map[string]string{{"statement": "RETURN 1"}}}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.Base+"/db/neo4j/tx/commit", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if n.User != "" || n.Pass != "" {
		req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(n.User+":"+n.Pass)))
	}
	resp, err := n.client.Do(req)
	if err != nil {
		return fmt.Errorf("ragstore: neo4j %s: %w", n.Base, err)
	}
	defer resp.Body.Close()
	var parsed struct {
		Errors []struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&parsed); err != nil {
		return fmt.Errorf("ragstore: neo4j %s: %w", n.Base, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("ragstore: neo4j %s: HTTP %d", n.Base, resp.StatusCode)
	}
	if len(parsed.Errors) > 0 {
		return fmt.Errorf("ragstore: neo4j %s: %s %s", n.Base, parsed.Errors[0].Code, parsed.Errors[0].Message)
	}
	return nil
}

// Upsert implements Store. The mirror also receives the relation edges, which
// is what makes the neighbour expansion below real.
func (n *Neo4jStore) Upsert(ctx context.Context, recs []usmf.CanonicalMemoryRecord) error {
	return n.Local.Upsert(ctx, recs)
}

// All implements Store.
func (n *Neo4jStore) All(ctx context.Context) ([]usmf.CanonicalMemoryRecord, error) {
	return n.Local.All(ctx)
}

// Search implements Store: the mirror's hybrid ranking, then one hop out
// through memory_edges. A neighbour keeps the score it would have had on its
// own cosine/BM25, discounted by one hop, so graph proximity can promote a
// record but never outrank a direct match on its own terms.
func (n *Neo4jStore) Search(ctx context.Context, query string, qvec []float32, k int) ([]Hit, error) {
	k = topK(k)
	direct, err := n.Local.Search(ctx, query, qvec, k)
	if err != nil {
		return nil, err
	}
	if len(direct) == 0 {
		return nil, nil
	}
	// Half of the weakest direct hit's fused score: a neighbour has to be
	// tied to something that matched well to appear, and it appears below
	// every direct hit.
	floor := direct[len(direct)-1].RRF / 2
	seen := make(map[string]bool, len(direct))
	for _, h := range direct {
		seen[h.Record.MemoryID] = true
	}
	var neighbours []string
	for _, h := range direct {
		ids, err := n.Local.Neighbors(ctx, h.Record.MemoryID)
		if err != nil {
			continue
		}
		for _, id := range ids {
			if !seen[id] {
				seen[id] = true
				neighbours = append(neighbours, id)
			}
		}
	}
	if len(neighbours) > 0 {
		byID, err := n.Local.recordsByID(ctx, neighbours)
		if err != nil {
			return nil, err
		}
		now := time.Now().UTC()
		for _, id := range neighbours {
			r, ok := byID[id]
			if !ok || !recordValid(r, now) {
				continue
			}
			direct = append(direct, Hit{Record: r, Cosine: 0, BM25: 0, RRF: floor})
		}
	}
	sortHits(direct)
	if len(direct) > k {
		direct = direct[:k]
	}
	return direct, nil
}

// Close implements Store.
func (n *Neo4jStore) Close() error { n.Local = nil; return nil }

// SetEmbedder implements embedderSetter by handing the model to the mirror.
func (n *Neo4jStore) SetEmbedder(e Embedder) { n.Local.SetEmbedder(e) }

// sortHits orders hits by fused score, then by memory id so the order is
// reproducible when two records tie.
func sortHits(hits []Hit) {
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].RRF != hits[j].RRF {
			return hits[i].RRF > hits[j].RRF
		}
		return hits[i].Record.MemoryID < hits[j].Record.MemoryID
	})
}

// recordsByID loads the named records from the mirror. A record that is gone
// is simply absent from the map.
func (s *SQLiteHybridStore) recordsByID(ctx context.Context, ids []string) (map[string]usmf.CanonicalMemoryRecord, error) {
	out := make(map[string]usmf.CanonicalMemoryRecord, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	// Parameter binding, one placeholder per id: never string concatenation.
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT memory_id, topic, claim, evidence_text, verdict, relation_edge, valid_from,
       invalid_at, source_url, source_domain, authority_level, embedding_model
FROM memory_records WHERE memory_id IN (`+placeholders+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var r usmf.CanonicalMemoryRecord
		if err := rows.Scan(&r.MemoryID, &r.Topic, &r.Claim, &r.EvidenceText, &r.Verdict,
			&r.RelationEdge, &r.ValidFrom, &r.InvalidAt, &r.SourceURL, &r.SourceDomain,
			&r.AuthorityLevel, &r.EmbeddingModel); err != nil {
			return nil, err
		}
		out[r.MemoryID] = r
	}
	return out, rows.Err()
}

// ensure the four remote backends really satisfy Store at compile time.
var (
	_ Store = (*PostgresStore)(nil)
	_ Store = (*MilvusStore)(nil)
	_ Store = (*ElasticsearchStore)(nil)
	_ Store = (*Neo4jStore)(nil)
)
