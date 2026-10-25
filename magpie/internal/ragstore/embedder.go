package ragstore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/yetone/magpie/internal/searchdet"
)

// DefaultDims is the embedding width both embedders use unless told
// otherwise: 1024 floats is small enough to keep a personal corpus in
// memory and wide enough that hash collisions between unrelated tokens are
// rare.
const DefaultDims = 1024

// Embedder turns text into a vector. Every store that keeps vectors calls
// one; HotSwap hands the target backend the same one so a migrated corpus
// stays in a single vector space.
type Embedder interface {
	// ID names the model, e.g. "local-ngram-1024" or "remote:Qwen3-Embedding".
	// It is written into each record's embedding_model.
	ID() string
	// Dim is the vector width the model produces.
	Dim() int
	// Embed returns one vector for one text.
	Embed(ctx context.Context, text string) ([]float32, error)
	// EmbedBatch returns one vector per text, in the same order.
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
}

// LocalHashEmbedder is a zero-dependency feature-hashing embedder: it needs
// no model file and no network, and the same text always produces the same
// vector, which is what makes the local stores reproducible across runs.
//
// Chinese is tokenised into character bigrams and trigrams (a whole CJK run
// would be one meaningless token), Latin text into lowercase words. Each
// token is FNV-1a hashed into one of Dims slots with a sign taken from the
// hash, accumulated, and the result is L2-normalised. This is the
// "hashing trick" from feature hashing: it is a real embedding in the sense
// that texts sharing tokens land close together, but it carries no learned
// semantics.
type LocalHashEmbedder struct {
	Dims int
}

// ID implements Embedder.
func (e *LocalHashEmbedder) ID() string {
	return fmt.Sprintf("local-ngram-%d", e.dim())
}

// Dim implements Embedder.
func (e *LocalHashEmbedder) Dim() int { return e.dim() }

func (e *LocalHashEmbedder) dim() int {
	if e.Dims <= 0 {
		return DefaultDims
	}
	return e.Dims
}

// Embed implements Embedder.
func (e *LocalHashEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	d := e.dim()
	vec := make([]float32, d)
	tokens := Tokenize(text)
	if len(tokens) == 0 {
		return vec, nil
	}
	for _, tok := range tokens {
		h := fnv.New64a()
		_, _ = h.Write([]byte(tok))
		sum := h.Sum64()
		idx := int(sum % uint64(d))
		sign := float32(1)
		// The sign comes from a different bit than the slot so the two are
		// independent: a slot's collisions cancel instead of piling up.
		if (sum>>62)&1 == 1 {
			sign = -1
		}
		vec[idx] += sign * tokenWeight(tok)
	}
	normalize(vec)
	return vec, nil
}

// EmbedBatch implements Embedder.
func (e *LocalHashEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v, err := e.Embed(ctx, t)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

// RemoteEmbedder calls an OpenAI-compatible /v1/embeddings endpoint. It is
// used when MOLA_EMBED_API_BASE is configured; otherwise the local hashing
// embedder carries the corpus offline.
type RemoteEmbedder struct {
	Base  string
	Key   string
	Model string
	Dims  int

	client *http.Client
}

// ID implements Embedder.
func (e *RemoteEmbedder) ID() string {
	m := e.Model
	if m == "" {
		m = "embedding"
	}
	return "remote:" + m
}

// Dim implements Embedder.
func (e *RemoteEmbedder) Dim() int {
	if e.Dims <= 0 {
		return DefaultDims
	}
	return e.Dims
}

type embedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type embedResponse struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (e *RemoteEmbedder) httpClient() *http.Client {
	if e.client != nil {
		return e.client
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// Embed implements Embedder by calling the endpoint with a one-item batch.
func (e *RemoteEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	vs, err := e.EmbedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(vs) == 0 {
		return nil, fmt.Errorf("ragstore: embedding endpoint returned nothing")
	}
	return vs[0], nil
}

// EmbedBatch implements Embedder. Texts are sent in chunks so one oversized
// request cannot fail a whole corpus.
func (e *RemoteEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	const chunk = 32
	out := make([][]float32, 0, len(texts))
	for start := 0; start < len(texts); start += chunk {
		end := start + chunk
		if end > len(texts) {
			end = len(texts)
		}
		part, err := e.embedChunk(ctx, texts[start:end])
		if err != nil {
			return nil, err
		}
		out = append(out, part...)
	}
	return out, nil
}

func (e *RemoteEmbedder) embedChunk(ctx context.Context, texts []string) ([][]float32, error) {
	body, err := json.Marshal(embedRequest{Model: e.Model, Input: texts})
	if err != nil {
		return nil, err
	}
	url := strings.TrimRight(e.Base, "/") + "/v1/embeddings"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if e.Key != "" {
		req.Header.Set("Authorization", "Bearer "+e.Key)
	}
	resp, err := e.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var parsed embedResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("ragstore: embedding reply from %s: %w", url, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := "no detail"
		if parsed.Error != nil && parsed.Error.Message != "" {
			msg = parsed.Error.Message
		}
		return nil, fmt.Errorf("ragstore: embedding endpoint %s: HTTP %d: %s", url, resp.StatusCode, msg)
	}
	if len(parsed.Data) != len(texts) {
		return nil, fmt.Errorf("ragstore: embedding endpoint %s returned %d vectors for %d texts", url, len(parsed.Data), len(texts))
	}
	// The endpoint may answer out of order; index is authoritative.
	sort.Slice(parsed.Data, func(i, j int) bool { return parsed.Data[i].Index < parsed.Data[j].Index })
	out := make([][]float32, len(parsed.Data))
	for i, d := range parsed.Data {
		out[i] = d.Embedding
	}
	return out, nil
}

// NewEmbedder picks the embedding model from the resolved keys: a remote
// endpoint when MOLA_EMBED_API_BASE is set, the local hashing embedder
// otherwise, so MolaSearch works with no key and no model download.
func NewEmbedder(k searchdet.Keys) Embedder {
	if strings.TrimSpace(k.EmbedAPIBase) != "" {
		return &RemoteEmbedder{
			Base:  strings.TrimRight(k.EmbedAPIBase, "/"),
			Key:   k.EmbedAPIKey,
			Model: k.EmbedModel,
			Dims:  DefaultDims,
		}
	}
	return &LocalHashEmbedder{Dims: DefaultDims}
}

// CosineSimilarity is the cosine of the angle between two vectors. A zero
// vector has no direction, so any comparison with one scores 0; vectors of
// different widths are not comparable and also score 0.
func CosineSimilarity(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		x, y := float64(a[i]), float64(b[i])
		dot += x * y
		na += x * x
		nb += y * y
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

func normalize(v []float32) {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	if sum == 0 {
		return
	}
	inv := float32(1 / math.Sqrt(sum))
	for i := range v {
		v[i] *= inv
	}
}

// Tokenize splits text into the tokens both the hashing embedder and the
// lexical scorer use, so a document's vector and its BM25 terms always agree
// on what its words are. CJK runs become character bigrams and trigrams
// (and a lone character stays itself); Latin runs become lowercase words.
// Punctuation and whitespace separate tokens and are dropped.
func Tokenize(text string) []string {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return nil
	}
	var tokens []string
	var cjk []rune
	var word []rune

	flushCJK := func() {
		if len(cjk) == 0 {
			return
		}
		if len(cjk) == 1 {
			tokens = append(tokens, string(cjk))
		}
		for i := 0; i+1 < len(cjk); i++ {
			tokens = append(tokens, string(cjk[i:i+2]))
		}
		for i := 0; i+2 < len(cjk); i++ {
			tokens = append(tokens, string(cjk[i:i+3]))
		}
		cjk = cjk[:0]
	}
	flushWord := func() {
		if len(word) == 0 {
			return
		}
		tokens = append(tokens, string(word))
		word = word[:0]
	}

	for _, r := range text {
		switch {
		case isCJK(r):
			flushWord()
			cjk = append(cjk, r)
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			flushCJK()
			word = append(word, r)
		default:
			flushCJK()
			flushWord()
		}
	}
	flushCJK()
	flushWord()
	return tokens
}

func isCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) ||
		unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) ||
		unicode.Is(unicode.Hangul, r)
}

// tokenWeight keeps a trigram, which overlaps its two bigrams, from
// outweighing the word it came from. A word or a bigram is the unit of
// meaning here.
func tokenWeight(tok string) float32 {
	n := len([]rune(tok))
	switch {
	case n == 1:
		return 0.5
	case n == 2:
		return 1
	case n == 3 && isCJKString(tok):
		return 0.6
	default:
		return 1
	}
}

func isCJKString(s string) bool {
	for _, r := range s {
		if !isCJK(r) {
			return false
		}
	}
	return true
}
