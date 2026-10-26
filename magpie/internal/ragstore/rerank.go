package ragstore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Reranker scores search results against a query. The store's default is its
// own RRF fusion (no Reranker); a remote one implements this interface.
type Reranker interface {
	// ID names the model, as the page shows it.
	ID() string
	// Rerank returns one score per document, in the order the documents
	// were given. Higher is more relevant.
	Rerank(ctx context.Context, query string, docs []string) ([]float64, error)
}

// RemoteReranker calls a Jina/Cohere-compatible POST {base}/rerank endpoint.
type RemoteReranker struct {
	Base  string
	Key   string
	Model string
}

// ID implements Reranker.
func (r *RemoteReranker) ID() string {
	if r.Model != "" {
		return r.Model
	}
	return "remote-reranker"
}

// rerankResponse accepts the shapes the compatible endpoints answer with:
// Jina's results[{index,relevance_score}] and Cohere's data[{index,score}].
type rerankResponse struct {
	Results []struct {
		Index          int     `json:"index"`
		RelevanceScore float64 `json:"relevance_score"`
	} `json:"results"`
	Data []struct {
		Index int     `json:"index"`
		Score float64 `json:"score"`
	} `json:"data"`
}

// Rerank implements Reranker.
func (r *RemoteReranker) Rerank(ctx context.Context, query string, docs []string) ([]float64, error) {
	if len(docs) == 0 {
		return nil, nil
	}
	body, err := json.Marshal(map[string]any{
		"model":     r.Model,
		"query":     query,
		"documents": docs,
		"top_n":     len(docs),
	})
	if err != nil {
		return nil, err
	}
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodPost, r.Base+"/rerank", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if r.Key != "" {
		req.Header.Set("Authorization", "Bearer "+r.Key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("rerank %s: HTTP %d: %s", r.Base, resp.StatusCode, snippet(raw, 200))
	}
	var out rerankResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("rerank %s: %w", r.Base, err)
	}
	scores := make([]float64, len(docs))
	seen := 0
	for _, item := range out.Results {
		if item.Index >= 0 && item.Index < len(scores) {
			scores[item.Index] = item.RelevanceScore
			seen++
		}
	}
	for _, item := range out.Data {
		if item.Index >= 0 && item.Index < len(scores) {
			scores[item.Index] = item.Score
			seen++
		}
	}
	if seen != len(docs) {
		return nil, fmt.Errorf("rerank %s answered %d of %d scores", r.Base, seen, len(docs))
	}
	return scores, nil
}

// snippet cuts a body down for an error message.
func snippet(b []byte, n int) string {
	s := string(b)
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
