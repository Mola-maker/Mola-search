// Package providerhub is the Tools & Providers page: search engines, query
// embedding, rerank, vision, speech, vector stores, models and agent tools
// in one registry, each with its key, its gate and a real connection check.
package providerhub

import "github.com/yetone/magpie/internal/pluginrt"

func init() {
	pluginrt.Register(pluginrt.Manifest{
		ID: "providerhub", API: "^1", Requires: []string{"window"},
		Name: "Tools & Providers",
		Sub:  "Search, embedding, rerank, vision, speech, vector stores and agent tools: one registry, keys in one place, every check a real call",
		One:  "1 provider connected", Many: "{n} providers connected",
		Parts:  []pluginrt.Part{{Slot: "nav.page", Key: "providerhub"}},
		Client: []string{"i18n.js", "client.js", "providerhub.css"},
	})
}
