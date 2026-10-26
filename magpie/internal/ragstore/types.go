// Package ragstore is MolaSearch's RAG memory layer. It keeps the verified
// facts an investigation produced in six different backends behind one
// Store interface, retrieves them with a real hybrid search (dense cosine +
// BM25 fused by reciprocal rank), and moves the whole corpus from one
// backend to another in three phases without stopping the reader.
//
// Every backend here is a real one: SQLite is a real database with FTS5 and
// WAL, the Markdown vault is real files on disk, and the four remote
// backends speak their own protocol (TCP for Postgres, REST for Milvus,
// Elasticsearch and Neo4j) while keeping a local SQLite mirror so a search
// still answers when the server is down. Nothing in this package is
// scripted data: scores come from vectors and term statistics computed here.
package ragstore

import (
	"context"

	"github.com/yetone/magpie/internal/usmf"
)

// Store is one RAG backend. Implementations must be safe for concurrent
// use by multiple goroutines.
type Store interface {
	// ID is the backend's stable identifier, the same string Status reports.
	ID() string
	// Ping checks that the backend is reachable right now. For a remote
	// backend this is a real network probe, not a cached flag.
	Ping(ctx context.Context) error
	// Upsert writes the records, replacing any with the same MemoryID.
	Upsert(ctx context.Context, recs []usmf.CanonicalMemoryRecord) error
	// All returns every stored record.
	All(ctx context.Context) ([]usmf.CanonicalMemoryRecord, error)
	// Search runs the hybrid retrieval for query and returns the best topK
	// hits, each carrying its real cosine, BM25 and RRF scores. qvec is the
	// query embedding; when it is empty the store embeds the query itself.
	Search(ctx context.Context, query string, qvec []float32, topK int) ([]Hit, error)
	// Close releases the backend's resources.
	Close() error
}

// Hit is one search result with the three scores that produced its rank.
// Rerank is the remote reranker's score when one is configured; zero says
// the ranking is the store's own RRF fusion.
type Hit struct {
	Record usmf.CanonicalMemoryRecord `json:"record"`
	Cosine float64                    `json:"cosine"`
	BM25   float64                    `json:"bm25"`
	RRF    float64                    `json:"rrf"`
	Rerank float64                    `json:"rerank,omitempty"`
}

// Vector is one stored embedding: the record it belongs to, the model that
// produced it and when it was last recomputed.
type Vector struct {
	MemoryID  string    `json:"memoryId"`
	Vec       []float32 `json:"vec"`
	Model     string    `json:"model"`
	UpdatedAt string    `json:"updatedAt"`
}

// Habit is one learned search habit, keyed by a query pattern. ModeAffinity
// says which investigation mode paid off for this pattern, and the domain
// lists remember which sources earned trust and which were content farms.
type Habit struct {
	Pattern        string             `json:"pattern"`
	ModeAffinity   map[string]float64 `json:"modeAffinity"`
	TrustedDomains []string           `json:"trustedDomains"`
	BlockedDomains []string           `json:"blockedDomains"`
	UpdatedAt      string             `json:"updatedAt"`
}

// embedderSetter is implemented by stores that compute their own vectors.
// HotSwap uses it to hand the target backend the embedding model the new
// corpus was built with, so the switch doesn't mix two vector spaces.
type embedderSetter interface {
	SetEmbedder(Embedder)
}

// counter is implemented by stores that can count their rows without
// materialising every record. Status prefers it over len(All).
type counter interface {
	Count(ctx context.Context) (int, error)
}
