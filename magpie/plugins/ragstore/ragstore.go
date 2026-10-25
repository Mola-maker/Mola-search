// Package ragstore is the RAGStore page: the six memory backends, the
// embedding and memory-mode choice, hybrid search with real scores, and the
// three-phase hot swap that moves every record without stopping.
package ragstore

import "github.com/yetone/magpie/internal/pluginrt"

func init() {
	pluginrt.Register(pluginrt.Manifest{
		ID: "ragstore", API: "^1", Requires: []string{"window"},
		Name: "RAG Store",
		Sub:  "Six memory backends, embedding and memory-mode choice, hybrid search and zero-downtime hot swap",
		One:  "1 memory record", Many: "{n} memory records",
		Parts:  []pluginrt.Part{{Slot: "nav.page", Key: "ragstore"}},
		Client: []string{"i18n.js", "client.js", "ragstore.css"},
	})
}
