// Package usmf is the record format MolaSearch's investigation engine and
// memory store share: one verified fact, its evidence, its verdict, its
// bi-temporal validity and the model that embedded it. It is deliberately
// its own package so the memory layer's Store contract does not depend on
// the retrieval engine, and a record can be exported or imported without
// either.
package usmf

// CanonicalMemoryRecord is one verified fact as every backend stores it.
type CanonicalMemoryRecord struct {
	MemoryID       string `json:"memoryId"`
	Topic          string `json:"topic"`
	Claim          string `json:"claim"`
	EvidenceText   string `json:"evidenceText"`
	Verdict        string `json:"verdict"`
	RelationEdge   string `json:"relationEdge"`
	ValidFrom      string `json:"validFrom"`
	InvalidAt      string `json:"invalidAt"`
	SourceURL      string `json:"sourceUrl"`
	SourceDomain   string `json:"sourceDomain"`
	AuthorityLevel int    `json:"authorityLevel"`
	EmbeddingModel string `json:"embeddingModel"`
}
