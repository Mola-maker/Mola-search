package ragstore

import (
	"math"
	"sort"
	"strings"
)

// bm25K1 and bm25B are the standard Okapi BM25 constants: k1 controls how
// fast repeated terms stop adding score, b saturates long documents. They
// are the values Lucene and FTS5 default to, so a score computed here is
// comparable to what the SQLite path gets from bm25(memory_fts).
const (
	bm25K1 = 1.2
	bm25B  = 0.75
)

// lexicalDoc is one document's term statistics for BM25.
type lexicalDoc struct {
	id     string
	tf     map[string]int
	length int
}

// lexicalIndex is an in-memory BM25 index over a corpus. It is what the
// SQLite store falls back to when FTS5 isn't compiled in, and what the
// Markdown vault uses (a directory of files has no search engine of its
// own). The scoring is the real Okapi formula, not a term count.
type lexicalIndex struct {
	docs  []lexicalDoc
	df    map[string]int
	avgdl float64
}

func newLexicalIndex(docs []lexicalDoc) *lexicalIndex {
	ix := &lexicalIndex{docs: docs, df: make(map[string]int, 64)}
	total := 0
	for _, d := range docs {
		total += d.length
		for term := range d.tf {
			ix.df[term]++
		}
	}
	if len(docs) > 0 {
		ix.avgdl = float64(total) / float64(len(docs))
	}
	return ix
}

// documentText is the searchable body of a record: the topic, the claim and
// the evidence, which is what a user is asking about when they search.
func documentText(rec recordFields) string {
	return strings.Join([]string{rec.topic, rec.claim, rec.evidenceText}, " ")
}

// recordFields is the subset of a record BM25 needs, so the scorer works for
// records read out of SQLite, out of a Markdown file or out of a mirror
// without converting them first.
type recordFields struct {
	topic        string
	claim        string
	evidenceText string
}

func fieldsOf(topic, claim, evidence string) recordFields {
	return recordFields{topic: topic, claim: claim, evidenceText: evidence}
}

func makeLexicalDoc(id, text string) lexicalDoc {
	tokens := Tokenize(text)
	tf := make(map[string]int, len(tokens))
	for _, t := range tokens {
		tf[t]++
	}
	return lexicalDoc{id: id, tf: tf, length: len(tokens)}
}

// score returns the BM25 score of doc for the query terms. Zero means the
// document shares no term with the query.
func (ix *lexicalIndex) score(queryTerms []string, doc lexicalDoc) float64 {
	if len(queryTerms) == 0 || len(ix.docs) == 0 {
		return 0
	}
	n := float64(len(ix.docs))
	avgdl := ix.avgdl
	if avgdl <= 0 {
		avgdl = 1
	}
	seen := make(map[string]bool, len(queryTerms))
	var sum float64
	for _, term := range queryTerms {
		if seen[term] {
			continue
		}
		seen[term] = true
		tf := float64(doc.tf[term])
		if tf == 0 {
			continue
		}
		df := float64(ix.df[term])
		idf := math.Log(1 + (n-df+0.5)/(df+0.5))
		denom := tf + bm25K1*(1-bm25B+bm25B*float64(doc.length)/avgdl)
		sum += idf * (tf * (bm25K1 + 1)) / denom
	}
	return sum
}

// rankDocuments scores every document and returns the ids ordered by score,
// highest first, leaving out the ones with no match.
func (ix *lexicalIndex) rankDocuments(query string) []string {
	terms := Tokenize(query)
	if len(terms) == 0 {
		return nil
	}
	type scored struct {
		id    string
		score float64
	}
	out := make([]scored, 0, len(ix.docs))
	for _, d := range ix.docs {
		if s := ix.score(terms, d); s > 0 {
			out = append(out, scored{d.id, s})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].score != out[j].score {
			return out[i].score > out[j].score
		}
		return out[i].id < out[j].id
	})
	ids := make([]string, len(out))
	for i, s := range out {
		ids[i] = s.id
	}
	return ids
}

// rrfConstant is the rank-fusion constant from the original Reciprocal Rank
// Fusion paper: 60 damped the top few ranks enough that a document both
// retrievers like beats one only a single retriever loves. It is also what
// Elasticsearch, Weaviate and Qdrant default to, so a fused score here means
// the same thing it means there.
const rrfConstant = 60

// fuseRanks applies reciprocal rank fusion to any number of ranked id lists
// and returns each id's fused score. A document missing from a list simply
// contributes nothing from it, which is how hybrid search lets either
// retriever promote a record the other missed.
func fuseRanks(lists ...[]string) map[string]float64 {
	fused := make(map[string]float64)
	for _, list := range lists {
		for rank, id := range list {
			fused[id] += 1 / float64(rrfConstant+rank+1)
		}
	}
	return fused
}

// topK normalises a caller's requested size: a non-positive or absurd value
// falls back to a sane default rather than returning everything.
func topK(k int) int {
	if k <= 0 {
		return 10
	}
	if k > 1000 {
		return 1000
	}
	return k
}
