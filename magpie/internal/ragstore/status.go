package ragstore

import (
	"fmt"
	"strings"
)

// The two memory modes the page may choose. Persistent is the default: facts
// and habits are kept in the active backend. Ephemeral keeps nothing new —
// searches still read what is already stored, but this run leaves no record
// and teaches no habit.
const (
	MemoryModePersistent = "persistent"
	MemoryModeEphemeral  = "ephemeral"
)

// RRFName is what ranks search results when no remote reranker is
// configured: reciprocal rank fusion of the dense and BM25 orders.
const RRFName = "RRF(k=60) 稠密+BM25 融合"

// EmbeddingModel names the model that is producing vectors right now.
func (m *Manager) EmbeddingModel() string { return m.emb.ID() }

// RerankerModel names the reranker applied to search results: the remote
// model when one is configured, otherwise the RRF fusion this store
// actually performs.
func (m *Manager) RerankerModel() string {
	m.choiceMu.RLock()
	defer m.choiceMu.RUnlock()
	if m.reranker == nil {
		return RRFName
	}
	return m.reranker.ID()
}

// MemoryMode is the write policy in force.
func (m *Manager) MemoryMode() string {
	m.choiceMu.RLock()
	defer m.choiceMu.RUnlock()
	if m.memoryMode == "" {
		return MemoryModePersistent
	}
	return m.memoryMode
}

// SetReranker switches the reranker. The empty string or the RRF name puts
// the store back on its own fusion; a remote model needs the endpoint it
// lives on, and a model other than the configured one is refused instead of
// silently swapped.
func (m *Manager) SetReranker(model string) error {
	model = strings.TrimSpace(model)
	if model == "" || model == RRFName {
		m.choiceMu.Lock()
		m.reranker = nil
		m.choiceMu.Unlock()
		return nil
	}
	base := strings.TrimSpace(m.keys.RerankAPIBase)
	if base == "" {
		return fmt.Errorf("重排模型 %q 需要 MOLA_RERANK_API_BASE，未配置时只能用 %s", model, RRFName)
	}
	if cfg := strings.TrimSpace(m.keys.RerankModel); cfg != "" && cfg != model {
		return fmt.Errorf("已配置的重排模型是 %q，不是 %q", cfg, model)
	}
	r := &RemoteReranker{Base: strings.TrimRight(base, "/"), Key: m.keys.RerankAPIKey, Model: model}
	m.choiceMu.Lock()
	m.reranker = r
	m.choiceMu.Unlock()
	return nil
}

// SetMemoryMode switches the write policy. Only the two modes this store
// implements are accepted.
func (m *Manager) SetMemoryMode(mode string) error {
	mode = strings.TrimSpace(mode)
	switch mode {
	case MemoryModePersistent, MemoryModeEphemeral:
		m.choiceMu.Lock()
		m.memoryMode = mode
		m.choiceMu.Unlock()
		return nil
	default:
		return fmt.Errorf("未知记忆模式 %q：只支持 %s / %s", mode, MemoryModePersistent, MemoryModeEphemeral)
	}
}
