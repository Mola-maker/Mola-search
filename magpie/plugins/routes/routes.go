// Package routes is the Search routes page: every reliable search path
// (local GitHub repository search, Exa, Tavily, Tencent/Yuanbao SearchPro,
// and the agent marketplace's skills, MCP servers, plugins and scripts),
// each route's last real request as a compact flow, the per-agent tech
// stacks, a swarm's conflict resolution, hot-plug of the built-in plugins,
// and the scheduled searches running on the engine's own ticker.
package routes

import "github.com/yetone/magpie/internal/pluginrt"

func init() {
	pluginrt.Register(pluginrt.Manifest{
		ID: "routes", API: "^1", Requires: []string{"window"},
		Name: "Search routes",
		Sub:  "Every reliable search path with its last real flow, per-agent stacks, multi-agent conflict resolution, hot-plug and scheduled search",
		One:  "1 scheduled search", Many: "{n} scheduled searches",
		Parts:  []pluginrt.Part{{Slot: "nav.page", Key: "routes"}},
		Client: []string{"i18n.js", "client.js", "routes.css"},
	})
}
